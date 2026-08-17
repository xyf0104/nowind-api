//go:build windows

package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeWindowsExecutable(t *testing.T) {
	got := normalizeWindowsExecutable(`"C:\Users\Test User\AppData\Local\Programs\Codex\Codex.exe",0`)
	want := `C:\Users\Test User\AppData\Local\Programs\Codex\Codex.exe`
	if got != want {
		t.Fatalf("normalizeWindowsExecutable() = %q, want %q", got, want)
	}
}

func TestWindowsCodexExecutableRejectsCommonCLIPaths(t *testing.T) {
	for _, candidate := range []string{
		`C:\Users\Test\AppData\Roaming\npm\codex.exe`,
		`C:\Users\Test\.cargo\bin\codex.exe`,
		`C:\Users\Test\scoop\apps\codex\current\codex.exe`,
		`C:\Program Files\Codex++\Codex.exe`,
		`C:\Users\Test\AppData\Local\Programs\CodexPlusPlus\Codex.exe`,
		`C:\Users\Administrator\.antigravity-ide\extensions\openai.chatgpt-26.721.30844-win32-x64\bin\windows-x86_64\codex.exe`,
		`C:\Users\Administrator\.vscode\extensions\openai.chatgpt-26.721.30844-win32-x64\bin\windows-x86_64\codex.exe`,
		`C:\Users\Administrator\AppData\Local\OpenAI\Codex\bin\3cff67e9f778ef0e\codex.exe`,
	} {
		if isWindowsCodexExecutable(candidate) {
			t.Fatalf("CLI path was detected as Codex App: %s", candidate)
		}
	}
}

func TestWindowsOfficialStorePackagePathRecognition(t *testing.T) {
	for _, candidate := range []string{
		`C:\Program Files\WindowsApps\OpenAI.Codex_26.810.4967.0_x64__2p2nqsd0c76g0`,
		`C:\Program Files\WindowsApps\OpenAI.Codex_26.810.4967.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe`,
	} {
		if !isOfficialWindowsCodexPackagePath(candidate) {
			t.Fatalf("official Store package path was not recognized: %s", candidate)
		}
	}
	if isOfficialWindowsCodexPackagePath(`C:\Users\Administrator\.antigravity-ide\extensions\openai.chatgpt-26.721.30844-win32-x64\bin\windows-x86_64\codex.exe`) {
		t.Fatal("Antigravity extension was recognized as an official Store package")
	}
}

func TestFilterOfficialWindowsCodexLaunchTargetsRejectsCodexPlusPlus(t *testing.T) {
	targets := filterOfficialWindowsCodexLaunchTargets([]string{
		`BigPizzaV3.CodexPlusPlus_abcd1234!App`,
		`OpenAI.Codex_2p2nqsd0c76g0!App`,
		`OpenAI.Codex_2p2nqsd0c76g0!App`,
		`xiass.codex-helper_1234!App`,
	})
	if len(targets) != 1 || targets[0] != `OpenAI.Codex_2p2nqsd0c76g0!App` {
		t.Fatalf("official launch targets = %v", targets)
	}
}

func TestOfficialWindowsCodexLaunchTargetRecognition(t *testing.T) {
	for _, target := range []string{
		`OpenAI.Codex_2p2nqsd0c76g0!App`,
		`  OPENAI.CODEX_2p2nqsd0c76g0!Codex  `,
	} {
		if !isOfficialWindowsCodexLaunchTarget(target) {
			t.Fatalf("official launch target was not recognized: %q", target)
		}
	}
	for _, target := range []string{
		`OpenAI.ChatGPT_2p2nqsd0c76g0!App`,
		`BigPizzaV3.CodexPlusPlus_abcd1234!App`,
		`OpenAI.Codex_2p2nqsd0c76g0`,
	} {
		if isOfficialWindowsCodexLaunchTarget(target) {
			t.Fatalf("unrelated launch target was accepted: %q", target)
		}
	}
}

func TestWindowsStoreCodexInstallationUsesOfficialLaunchTarget(t *testing.T) {
	installation, ok := windowsStoreCodexInstallation([]string{
		`BigPizzaV3.CodexPlusPlus_abcd1234!App`,
		`OpenAI.Codex_2p2nqsd0c76g0!App`,
	})
	if !ok || !installation.Found {
		t.Fatalf("Store Codex installation was not detected: %+v", installation)
	}
	if installation.LaunchTarget != `OpenAI.Codex_2p2nqsd0c76g0!App` {
		t.Fatalf("Store launch target = %q", installation.LaunchTarget)
	}
	if installation.Executable != "" {
		t.Fatalf("protected Store executable should not be launched directly: %q", installation.Executable)
	}
}

func TestWindowsPackagedExecutableDetection(t *testing.T) {
	packaged := `C:\Program Files\WindowsApps\OpenAI.Codex_26.707.12708.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe`
	if !isWindowsPackagedExecutable(packaged) {
		t.Fatalf("Windows Store Codex path was not detected: %s", packaged)
	}
	if isWindowsPackagedExecutable(`C:\Program Files\Codex\Codex.exe`) {
		t.Fatal("ordinary Codex installation was misclassified as a Store package")
	}
}

func TestWindowsNativeProcessLookupFindsCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processIDs, err := windowsProcessIDsWithError(executable)
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(os.Getpid())
	for _, processID := range processIDs {
		if processID == want {
			return
		}
	}
	t.Fatalf("current process %s was not found in %v", want, processIDs)
}

func TestHiddenWindowsCommandUsesNoWindowFlags(t *testing.T) {
	command := hiddenWindowsCommand("cmd.exe", "/c", "exit", "0")
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("hidden command is missing the Windows hide-window flag")
	}
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsTaskkillArgumentsEscalateToForce(t *testing.T) {
	graceful := windowsTaskkillArguments("123", false)
	forced := windowsTaskkillArguments("123", true)
	if got := strings.Join(graceful, " "); got != "/PID 123 /T" {
		t.Fatalf("graceful taskkill arguments = %q", got)
	}
	if got := strings.Join(forced, " "); got != "/PID 123 /T /F" {
		t.Fatalf("forced taskkill arguments = %q", got)
	}
}

func TestWindowsStoreCodexProcessIDsIncludePathlessBackgroundApp(t *testing.T) {
	processIDs := windowsStoreCodexProcessIDs([]windowsProcess{
		{ID: 11, Name: "ChatGPT.exe"},
		{ID: 12, Name: "ChatGPT.exe", Path: `C:\Program Files\WindowsApps\OpenAI.Codex_26.810.4967.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe`},
		{ID: 13, Name: "ChatGPT.exe", Path: `C:\Other\ChatGPT.exe`},
		{ID: 14, Name: "codex.exe", Path: `C:\Users\Test\AppData\Roaming\npm\codex.exe`},
	})
	if got := strings.Join(processIDs, ","); got != "11,12" {
		t.Fatalf("Store Codex process IDs = %q, want pathless and official ChatGPT processes", got)
	}
}
