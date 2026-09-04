package admin

import (
	"bytes"
	"encoding/base64"
	stdhtml "html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	teamMailboxMIMEMaxDecodedBytes = 512 * 1024
	teamMailboxMIMEMaxDepth        = 8
	teamMailboxMIMEMaxParts        = 64
)

var teamMailboxMIMEContentKeys = []string{
	"text", "plain", "plain_text", "body_text", "body", "content", "message", "html", "preview", "snippet",
	"raw", "raw_message", "rawMessage", "source", "mime", "original",
}

type teamMailboxMIMETextParts struct {
	plain []string
	html  []string
}

type teamMailboxDecodedMIMEContent struct {
	Text       string
	HTML       string
	Recognized bool
}

// teamMailboxDecodeMIMEHeader makes RFC 2047 encoded words readable before
// they reach the public inbox. Invalid headers stay visible verbatim instead
// of hiding a subject or sender.
func teamMailboxDecodeMIMEHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoder := &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil || strings.TrimSpace(decoded) == "" {
		return value
	}
	return strings.TrimSpace(decoded)
}

// teamMailboxDecodeMIMEContent accepts both complete RFC 5322 messages and
// multipart bodies returned by several mailbox Workers after they strip the
// outer message headers. It retains a separately sanitized HTML alternative
// when one exists, while Text remains the readable fallback used by polling
// and previews.
func teamMailboxDecodeMIMEContent(raw string) teamMailboxDecodedMIMEContent {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		return teamMailboxDecodedMIMEContent{}
	}

	if teamMailboxLooksLikeCompleteMIME(raw) {
		message, err := mail.ReadMessage(strings.NewReader(raw))
		if err == nil {
			parts := teamMailboxMIMETextParts{}
			teamMailboxCollectMIMEEntity(&parts, textproto.MIMEHeader(message.Header), message.Body, 0)
			return parts.content(true)
		}
	}

	if boundary := teamMailboxMultipartBodyBoundary(raw); boundary != "" {
		parts := teamMailboxMIMETextParts{}
		reader := multipart.NewReader(strings.NewReader(raw), boundary)
		teamMailboxCollectMultipartParts(&parts, reader, 0)
		return parts.content(true)
	}

	return teamMailboxDecodedMIMEContent{}
}

// teamMailboxDecodeMIMEText preserves the original text-only contract for
// code polling and legacy callers. The dedicated HTML field is only used by
// the public mail reader after it has gone through the server sanitizer.
func teamMailboxDecodeMIMEText(raw string) (string, bool) {
	content := teamMailboxDecodeMIMEContent(raw)
	return content.Text, content.Recognized
}

func teamMailboxLooksLikeCompleteMIME(raw string) bool {
	headerEnd := strings.Index(raw, "\r\n\r\n")
	separatorLength := len("\r\n\r\n")
	if headerEnd < 0 {
		headerEnd = strings.Index(raw, "\n\n")
		separatorLength = len("\n\n")
	}
	if headerEnd <= 0 || headerEnd+separatorLength > len(raw) || headerEnd > 32*1024 {
		return false
	}
	headers := strings.ToLower(raw[:headerEnd])
	for _, name := range []string{"mime-version", "content-type", "content-transfer-encoding", "from", "subject"} {
		if strings.HasPrefix(headers, name+":") || strings.Contains(headers, "\n"+name+":") {
			return true
		}
	}
	return false
}

func teamMailboxMultipartBodyBoundary(raw string) string {
	lineEnd := strings.IndexByte(raw, '\n')
	if lineEnd < 0 {
		return ""
	}
	line := strings.TrimSpace(strings.TrimSuffix(raw[:lineEnd], "\r"))
	if !strings.HasPrefix(line, "--") || strings.HasSuffix(line, "--") {
		return ""
	}
	boundary := strings.TrimSpace(strings.TrimPrefix(line, "--"))
	if boundary == "" || len(boundary) > 200 || strings.ContainsAny(boundary, "\r\n") {
		return ""
	}
	// Avoid treating ordinary markdown or a plain text separator as a MIME
	// boundary. Real parts must introduce their own MIME headers immediately.
	remainder := strings.ToLower(raw[lineEnd+1:])
	if len(remainder) > 1024 {
		remainder = remainder[:1024]
	}
	if !strings.Contains(remainder, "content-type:") {
		return ""
	}
	return boundary
}

func teamMailboxCollectMIMEEntity(parts *teamMailboxMIMETextParts, header textproto.MIMEHeader, body io.Reader, depth int) {
	if parts == nil || body == nil || depth > teamMailboxMIMEMaxDepth {
		return
	}
	mediaType := "text/plain"
	params := map[string]string{}
	if contentType := strings.TrimSpace(header.Get("Content-Type")); contentType != "" {
		parsedType, parsedParams, err := mime.ParseMediaType(contentType)
		if err != nil {
			return
		}
		mediaType = strings.ToLower(strings.TrimSpace(parsedType))
		params = parsedParams
		if mediaType == "" {
			mediaType = "text/plain"
		}
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := strings.TrimSpace(params["boundary"])
		if boundary == "" {
			return
		}
		teamMailboxCollectMultipartParts(parts, multipart.NewReader(body, boundary), depth+1)
		return
	}

	if mediaType == "message/rfc822" {
		raw, err := readTeamMailboxMIMEBytes(body)
		if err != nil {
			return
		}
		message, err := mail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			return
		}
		teamMailboxCollectMIMEEntity(parts, textproto.MIMEHeader(message.Header), message.Body, depth+1)
		return
	}

	if mediaType != "text/plain" && mediaType != "text/html" {
		return
	}
	if teamMailboxMIMEAttachment(header) || strings.TrimSpace(params["name"]) != "" {
		return
	}

	text, err := teamMailboxDecodeMIMEBody(body, header.Get("Content-Transfer-Encoding"), params["charset"])
	if err != nil {
		return
	}
	text = stdhtml.UnescapeString(text)
	if mediaType == "text/html" {
		text = strings.TrimSpace(text)
	} else {
		text = strings.TrimSpace(text)
	}
	if text == "" {
		return
	}
	if mediaType == "text/plain" {
		parts.plain = append(parts.plain, text)
		return
	}
	parts.html = append(parts.html, text)
}

func teamMailboxCollectMultipartParts(parts *teamMailboxMIMETextParts, reader *multipart.Reader, depth int) {
	if parts == nil || reader == nil || depth > teamMailboxMIMEMaxDepth {
		return
	}
	for index := 0; index < teamMailboxMIMEMaxParts; index++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		teamMailboxCollectMIMEEntity(parts, part.Header, part, depth+1)
		_ = part.Close()
	}
}

func teamMailboxMIMEAttachment(header textproto.MIMEHeader) bool {
	disposition, params, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(disposition), "attachment") || strings.TrimSpace(params["filename"]) != ""
}

func teamMailboxDecodeMIMEBody(body io.Reader, transferEncoding, charsetName string) (string, error) {
	reader := body
	switch strings.ToLower(strings.TrimSpace(transferEncoding)) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		reader = quotedprintable.NewReader(body)
	}
	raw, err := readTeamMailboxMIMEBytes(reader)
	if err != nil {
		return "", err
	}
	charsetName = strings.TrimSpace(charsetName)
	if charsetName == "" || strings.EqualFold(charsetName, "utf-8") || strings.EqualFold(charsetName, "us-ascii") {
		return string(raw), nil
	}
	converted, err := charset.NewReaderLabel(charsetName, bytes.NewReader(raw))
	if err != nil {
		// Unknown legacy charsets should not turn an otherwise readable message
		// into an empty inbox entry.
		return string(raw), nil
	}
	decoded, err := readTeamMailboxMIMEBytes(converted)
	if err != nil {
		return string(raw), nil
	}
	return string(decoded), nil
}

func readTeamMailboxMIMEBytes(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, io.EOF
	}
	body, err := io.ReadAll(io.LimitReader(reader, teamMailboxMIMEMaxDecodedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > teamMailboxMIMEMaxDecodedBytes {
		return nil, io.ErrShortBuffer
	}
	return body, nil
}

func (parts teamMailboxMIMETextParts) text() string {
	values := parts.plain
	if len(values) == 0 {
		return teamMailboxTextFromHTML(parts.htmlText())
	}
	return strings.Join(uniqueTeamMailboxMIMEStrings(values), "\n\n")
}

func (parts teamMailboxMIMETextParts) htmlText() string {
	return strings.Join(uniqueTeamMailboxMIMEStrings(parts.html), "\n")
}

func (parts teamMailboxMIMETextParts) content(recognized bool) teamMailboxDecodedMIMEContent {
	return teamMailboxDecodedMIMEContent{
		Text:       parts.text(),
		HTML:       teamMailboxSanitizeHTML(parts.htmlText()),
		Recognized: recognized,
	}
}

func uniqueTeamMailboxMIMEStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

var teamMailboxSafeHTMLTags = map[string]struct{}{
	"a": {}, "b": {}, "blockquote": {}, "br": {}, "center": {}, "code": {}, "div": {}, "em": {},
	"font": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {}, "hr": {}, "i": {},
	"img": {}, "li": {}, "ol": {}, "p": {}, "pre": {}, "s": {}, "small": {}, "span": {}, "strong": {},
	"style": {}, "sub": {}, "sup": {}, "table": {}, "tbody": {}, "td": {}, "tfoot": {}, "th": {},
	"thead": {}, "tr": {}, "u": {}, "ul": {},
}

var teamMailboxVoidHTMLTags = map[string]struct{}{
	"br": {}, "hr": {}, "img": {},
}

var teamMailboxDiscardedHTMLTags = map[string]struct{}{
	"audio": {}, "base": {}, "button": {}, "canvas": {}, "embed": {}, "form": {}, "head": {},
	"iframe": {}, "input": {}, "link": {}, "math": {}, "meta": {}, "noscript": {},
	"object": {}, "picture": {}, "script": {}, "select": {}, "source": {}, "svg": {},
	"template": {}, "textarea": {}, "title": {}, "track": {}, "video": {},
}

var teamMailboxSafeHTMLStyleProperties = map[string]struct{}{
	"background": {}, "background-color": {}, "border": {}, "border-bottom": {}, "border-collapse": {},
	"border-color": {}, "border-left": {}, "border-radius": {}, "border-right": {}, "border-spacing": {},
	"border-style": {}, "border-top": {}, "border-width": {}, "box-sizing": {}, "color": {}, "display": {},
	"font": {}, "font-family": {}, "font-size": {}, "font-style": {}, "font-weight": {}, "height": {},
	"letter-spacing": {}, "line-height": {}, "margin": {}, "margin-bottom": {}, "margin-left": {},
	"margin-right": {}, "margin-top": {}, "max-height": {}, "max-width": {}, "min-height": {},
	"min-width": {}, "opacity": {}, "overflow": {}, "overflow-wrap": {}, "overflow-x": {}, "overflow-y": {},
	"padding": {}, "padding-bottom": {}, "padding-left": {}, "padding-right": {}, "padding-top": {},
	"table-layout": {}, "text-align": {}, "text-decoration": {}, "text-overflow": {}, "text-transform": {},
	"vertical-align": {}, "white-space": {}, "width": {}, "word-break": {},
}

// teamMailboxSanitizeHTML keeps the useful visual structure from ordinary
// email templates without letting a message run scripts, submit forms, load
// external styles, or use layout-breaking CSS inside the long-lived public
// inbox. Visible images follow a constrained source policy and tracking beacons
// are removed. The browser applies a second independent sanitizer before
// rendering.
func teamMailboxSanitizeHTML(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > teamMailboxMIMEMaxDecodedBytes {
		return ""
	}
	document, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	var output strings.Builder
	output.Grow(len(raw))
	teamMailboxWriteSafeHTML(&output, document)
	return strings.TrimSpace(output.String())
}

func teamMailboxWriteSafeHTML(output *strings.Builder, node *xhtml.Node) {
	if output == nil || node == nil {
		return
	}
	switch node.Type {
	case xhtml.TextNode:
		_, _ = output.WriteString(stdhtml.EscapeString(node.Data))
		return
	case xhtml.ElementNode:
		tag := strings.ToLower(strings.TrimSpace(node.Data))
		if tag == "head" {
			// HTML parsers commonly move email stylesheet blocks into <head>.
			// Keep only those styles; metadata, titles, and every other head child
			// remain outside the public mail document.
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == xhtml.ElementNode && strings.EqualFold(strings.TrimSpace(child.Data), "style") {
					teamMailboxWriteSafeHTML(output, child)
				}
			}
			return
		}
		if _, discarded := teamMailboxDiscardedHTMLTags[tag]; discarded {
			return
		}
		if tag == "img" && teamMailboxHTMLImageIsTrackingPixel(node.Attr) {
			return
		}
		if tag == "style" {
			if stylesheet := teamMailboxSafeHTMLStyleSheet(teamMailboxHTMLNodeText(node)); stylesheet != "" {
				_, _ = output.WriteString("<style>")
				_, _ = output.WriteString(stylesheet)
				_, _ = output.WriteString("</style>")
			}
			return
		}
		_, allowed := teamMailboxSafeHTMLTags[tag]
		if allowed {
			_ = output.WriteByte('<')
			_, _ = output.WriteString(tag)
			for _, attribute := range teamMailboxSafeHTMLAttributes(tag, node.Attr) {
				_ = output.WriteByte(' ')
				_, _ = output.WriteString(attribute.name)
				_, _ = output.WriteString(`="`)
				_, _ = output.WriteString(stdhtml.EscapeString(attribute.value))
				_ = output.WriteByte('"')
			}
			_ = output.WriteByte('>')
			if _, void := teamMailboxVoidHTMLTags[tag]; void {
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			teamMailboxWriteSafeHTML(output, child)
		}
		if allowed {
			_, _ = output.WriteString("</")
			_, _ = output.WriteString(tag)
			_ = output.WriteByte('>')
		}
		return
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		teamMailboxWriteSafeHTML(output, child)
	}
}

type teamMailboxSafeHTMLAttribute struct {
	name  string
	value string
}

func teamMailboxSafeHTMLAttributes(tag string, attributes []xhtml.Attribute) []teamMailboxSafeHTMLAttribute {
	if tag == "img" && teamMailboxHTMLImageIsTrackingPixel(attributes) {
		return nil
	}
	result := make([]teamMailboxSafeHTMLAttribute, 0, 6)
	var href string
	for _, attribute := range attributes {
		name := strings.ToLower(strings.TrimSpace(attribute.Key))
		value := strings.TrimSpace(attribute.Val)
		if value == "" {
			continue
		}
		switch name {
		case "href":
			if tag == "a" {
				href = value
			}
		case "src":
			if tag == "img" {
				if source, ok := teamMailboxSafeHTMLImageSource(value); ok {
					result = append(result, teamMailboxSafeHTMLAttribute{name: "src", value: source})
				}
			}
		case "style":
			if style := teamMailboxSafeHTMLStyle(value); style != "" {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "style", value: style})
			}
		case "class":
			if className, ok := teamMailboxSafeHTMLClass(value); ok {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "class", value: className})
			}
		case "id":
			if identifier, ok := teamMailboxSafeHTMLIdentifier(value); ok {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "id", value: identifier})
			}
		case "alt":
			if tag == "img" && len(value) <= 1024 && teamMailboxHTMLHasNoControls(value) {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "alt", value: value})
			}
		case "role":
			if tag == "img" && (value == "img" || value == "presentation" || value == "none") {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "role", value: value})
			}
		case "title":
			if len(value) <= 512 && teamMailboxHTMLHasNoControls(value) {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "title", value: value})
			}
		case "align":
			if teamMailboxSafeHTMLAlign(value) {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "align", value: strings.ToLower(value)})
			}
		case "valign":
			if teamMailboxSafeHTMLValign(value) {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "valign", value: strings.ToLower(value)})
			}
		case "width", "height":
			if dimension, ok := teamMailboxSafeHTMLDimension(value); ok {
				result = append(result, teamMailboxSafeHTMLAttribute{name: name, value: dimension})
			}
		case "colspan", "rowspan", "cellpadding", "cellspacing", "border":
			if number, ok := teamMailboxSafeHTMLInteger(value); ok {
				result = append(result, teamMailboxSafeHTMLAttribute{name: name, value: number})
			}
		case "bgcolor":
			if color, ok := teamMailboxSafeHTMLColor(value); ok {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "bgcolor", value: color})
			}
		case "dir":
			if direction := strings.ToLower(value); direction == "ltr" || direction == "rtl" || direction == "auto" {
				result = append(result, teamMailboxSafeHTMLAttribute{name: "dir", value: direction})
			}
		}
	}
	if tag == "a" {
		if safeHref, ok := teamMailboxSafeHTMLHref(href); ok {
			result = append(result,
				teamMailboxSafeHTMLAttribute{name: "href", value: safeHref},
				teamMailboxSafeHTMLAttribute{name: "target", value: "_blank"},
				teamMailboxSafeHTMLAttribute{name: "rel", value: "noopener noreferrer nofollow"},
			)
		}
	}
	return result
}

func teamMailboxHTMLNodeText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	var output strings.Builder
	var visit func(*xhtml.Node)
	visit = func(current *xhtml.Node) {
		if current == nil {
			return
		}
		if current.Type == xhtml.TextNode {
			output.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return output.String()
}

func teamMailboxHTMLImageIsTrackingPixel(attributes []xhtml.Attribute) bool {
	width, height := "", ""
	for _, attribute := range attributes {
		switch strings.ToLower(strings.TrimSpace(attribute.Key)) {
		case "width":
			width = attribute.Val
		case "height":
			height = attribute.Val
		}
	}
	return teamMailboxHTMLSmallDimension(width) && teamMailboxHTMLSmallDimension(height)
}

func teamMailboxHTMLSmallDimension(value string) bool {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "px"))
	if value == "" || strings.HasSuffix(value, "%") {
		return false
	}
	dimension, err := strconv.ParseFloat(value, 64)
	return err == nil && dimension >= 0 && dimension <= 2
}

func teamMailboxSafeHTMLImageSource(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > teamMailboxMIMEMaxDecodedBytes || !teamMailboxHTMLHasNoControls(value) || strings.Contains(value, `\`) {
		return "", false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "data:image/") {
		if strings.HasPrefix(lower, "data:image/png;") || strings.HasPrefix(lower, "data:image/jpeg;") || strings.HasPrefix(lower, "data:image/gif;") || strings.HasPrefix(lower, "data:image/webp;") {
			return value, true
		}
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.String(), true
	default:
		return "", false
	}
}

func teamMailboxSafeHTMLClass(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 32 {
		return "", false
	}
	for _, part := range parts {
		if _, ok := teamMailboxSafeHTMLIdentifier(part); !ok {
			return "", false
		}
	}
	return strings.Join(parts, " "), true
}

func teamMailboxSafeHTMLIdentifier(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "", false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' || character == '-' {
			continue
		}
		return "", false
	}
	return value, true
}

func teamMailboxSafeHTMLHref(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 || !teamMailboxHTMLHasNoControls(value) || strings.Contains(value, `\`) {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return parsed.String(), true
	default:
		return "", false
	}
}

func teamMailboxSafeHTMLStyle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 {
		return ""
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"url(", "expression(", "@import", "behavior:", "-moz-binding", "javascript:", "vbscript:", "var(", "calc(", "<", ">", "\\"} {
		if strings.Contains(lower, forbidden) {
			return ""
		}
	}
	values := make([]string, 0, 8)
	for _, declaration := range strings.Split(value, ";") {
		name, rawValue, found := strings.Cut(declaration, ":")
		if !found {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if _, allowed := teamMailboxSafeHTMLStyleProperties[name]; !allowed {
			continue
		}
		rawValue = strings.TrimSpace(rawValue)
		if rawValue == "" || len(rawValue) > 256 || !teamMailboxSafeHTMLStyleValue(rawValue) {
			continue
		}
		values = append(values, name+":"+rawValue)
	}
	return strings.Join(values, "; ")
}

func teamMailboxSafeHTMLStyleValue(value string) bool {
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			continue
		}
		if strings.ContainsRune(" !#%.,()+-/'\"", character) {
			continue
		}
		return false
	}
	return true
}

// Style tags are retained only for ordinary email layout. The rules remain
// isolated inside the sandboxed mail document, and each declaration passes the
// same property/value filter used for inline styles.
func teamMailboxSafeHTMLStyleSheet(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 32*1024 {
		return ""
	}
	values := make([]string, 0, 8)
	for len(value) > 0 {
		opening := strings.IndexByte(value, '{')
		if opening < 0 {
			break
		}
		selector := strings.TrimSpace(value[:opening])
		value = value[opening+1:]
		closing := strings.IndexByte(value, '}')
		if closing < 0 {
			break
		}
		declarations := value[:closing]
		value = value[closing+1:]
		if !teamMailboxSafeHTMLStyleSelector(selector) {
			continue
		}
		if safeDeclarations := teamMailboxSafeHTMLStyle(declarations); safeDeclarations != "" {
			values = append(values, selector+"{"+safeDeclarations+"}")
		}
	}
	return strings.Join(values, "\n")
}

func teamMailboxSafeHTMLStyleSelector(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 {
		return false
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"@", "url(", "expression", "behavior", "-moz-binding", "<", ">"} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			continue
		}
		if strings.ContainsRune(" .#_-,:+~[]='\"*", character) {
			continue
		}
		return false
	}
	return true
}

func teamMailboxSafeHTMLDimension(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 12 {
		return "", false
	}
	percent := strings.HasSuffix(value, "%")
	number := strings.TrimSuffix(value, "%")
	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil || parsed < 0 {
		return "", false
	}
	if percent && parsed <= 100 {
		return value, true
	}
	if !percent && parsed <= 5000 {
		return value, true
	}
	return "", false
}

func teamMailboxSafeHTMLInteger(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 6 {
		return "", false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 5000 {
		return "", false
	}
	return strconv.Itoa(parsed), true
}

func teamMailboxSafeHTMLColor(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 32 || !teamMailboxHTMLHasNoControls(value) {
		return "", false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '#' {
			continue
		}
		return "", false
	}
	return value, true
}

func teamMailboxSafeHTMLAlign(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "left", "right", "center", "justify":
		return true
	default:
		return false
	}
}

func teamMailboxSafeHTMLValign(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "top", "middle", "bottom", "baseline":
		return true
	default:
		return false
	}
}

func teamMailboxHTMLHasNoControls(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func teamMailboxLooksLikeHTML(value string) bool {
	for index := 0; index+1 < len(value); index++ {
		if value[index] != '<' {
			continue
		}
		next := index + 1
		if value[next] == '/' {
			next++
		}
		if next < len(value) && ((value[next] >= 'a' && value[next] <= 'z') || (value[next] >= 'A' && value[next] <= 'Z')) {
			return true
		}
	}
	return false
}

// teamMailboxDecodedMIMETextFromMessage only returns text from values that
// were positively recognized as MIME. It supplements the existing generic
// mailbox extractor for automatic OAuth code polling without changing normal
// provider payloads that already expose plain text fields.
func teamMailboxDecodedMIMETextFromMessage(message map[string]any) string {
	parts := make([]string, 0, 4)
	for _, key := range teamMailboxMIMEContentKeys {
		appendTeamMailboxDecodedMIMEValue(&parts, message[key], 0)
	}
	return strings.Join(uniqueTeamMailboxMIMEStrings(parts), "\n\n")
}

func appendTeamMailboxDecodedMIMEValue(parts *[]string, value any, depth int) {
	if depth > teamMailboxMIMEMaxDepth || value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		if decoded, recognized := teamMailboxDecodeMIMEText(typed); recognized && decoded != "" {
			*parts = append(*parts, decoded)
		}
	case []any:
		for _, item := range typed {
			appendTeamMailboxDecodedMIMEValue(parts, item, depth+1)
		}
	case map[string]any:
		for _, key := range teamMailboxMIMEContentKeys {
			if nested, ok := typed[key]; ok {
				appendTeamMailboxDecodedMIMEValue(parts, nested, depth+1)
			}
		}
	}
}
