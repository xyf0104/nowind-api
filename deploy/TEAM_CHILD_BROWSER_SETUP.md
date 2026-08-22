# Team 子号创建：部署与使用教程

这份教程适用于已部署 XIASS 的管理员。它会在“账号管理”里提供一个 `创建 Team 子号` 入口：左侧创建一次性临时邮箱并轮询验证码，右侧嵌入一台服务器上的 Chromium 浏览器。浏览器的登录状态保留在服务器上，因此通常只需首次登录一次。

该功能一次只处理一个由管理员明确发起的流程。外部站点中的 CAPTCHA、短信、身份验证、手机验证、工作区确认及最终第三方操作仍需在可见浏览器中由管理员完成；XIASS 不会尝试绕过这些步骤。

## 是否还需要加代码？

不需要。此版本已经包含：

- 管理端页面、`创建 Team 子号` 高亮入口和导入 OpenAI OAuth 账号接口；
- 服务端临时邮箱创建与验证码轮询；
- 服务器 Chromium 容器、持久化浏览器配置目录和同源 iframe 代理；
- 网页导入私有邮箱配置文件，并将配置仅保存到 XIASS 持久化数据目录。

上线时只需要完成下面的配置并启动 Compose profile。不要自行给 Chromium 增加 `ports:`；浏览器只应该通过 XIASS 管理页访问。

## 1. 前置条件

- 使用 XIASS 推荐的 Docker 部署，目录通常为 `/opt/xiass-api/deploy`。
- 服务器已经安装 Docker Compose。新服务器可使用仓库的 `xiass-install.sh` 完成基础安装。
- 一个你有权使用的临时邮箱服务。XIASS 需要能调用“创建邮箱”和“读取邮件”接口。
- 管理员可访问 XIASS 后台，以及待操作的第三方工作区。

当前电脑没有 Docker，因此本地只能验证页面、构建和单元测试；实际 Chromium 容器应在目标服务器启动。

## 2. 部署这版源码

在服务器上更新至包含本功能的源码后，进入部署目录：

```bash
cd /opt/xiass-api/deploy
git -C .. status --short --branch
```

如当前实例并非使用 `/opt/xiass-api`，将下文命令中的路径替换为实际部署目录。更新前先保留 `.env`、`data`、`postgres_data`、`redis_data` 和 `team_child_browser_data`；不要使用 `docker compose down -v`。

## 3. 配置临时邮箱服务

邮箱服务凭据只保存在服务器持久化数据目录，不会写入数据库或回显到管理页。首次部署还没有 `.env` 时，先从模板创建；然后备份并限制权限：

```bash
cd /opt/xiass-api/deploy
test -f .env || cp .env.example .env
cp .env ".env.before-team-child.$(date +%Y%m%d-%H%M%S)"
chmod 600 .env
```

### 直接在网页导入配置文件

登录 XIASS 管理后台，进入 `账号管理` -> `创建 Team 子号`，点击页面右上角的 `导入邮箱配置`，选择你的私有邮箱配置文件。服务端会验证并规范化该配置，将其保存到 `/app/data/team-child-mail.env`（宿主机目录部署对应 `deploy/data/team-child-mail.env`），页面不会显示凭据，导入后无需重启即可开始创建邮箱。

网页上传只接受管理员会话和 256 KB 以内的单个文件。发布仓库、镜像、Compose 模板和 `.env.example` 都不携带导入后的配置。若你的部署使用只读数据目录，网页会提示保存失败；修复 `data` 目录权限后重新在网页导入即可。

## 4. 启动服务器 Chromium 浏览器

继续编辑同一份 `.env`，添加或修改：

```dotenv
TEAM_CHILD_BROWSER_ENABLED=true
TEAM_CHILD_BROWSER_UPSTREAM_URL=https://team-child-browser:3001
TEAM_CHILD_BROWSER_SESSION_TTL_MINUTES=720
TEAM_CHILD_BROWSER_TICKET_TTL_SECONDS=180
TEAM_CHILD_BROWSER_CONTROL_TTL_SECONDS=120
TEAM_CHILD_BROWSER_START_URL=https://chatgpt.com/admin/members
TEAM_CHILD_BROWSER_PUID=1000
TEAM_CHILD_BROWSER_PGID=1000
TEAM_CHILD_AUTOMATION_URL=http://team-child-browser:8090
# 使用 `openssl rand -hex 32` 生成；同一个值必须同时配置给 XIASS 和自动化容器。
TEAM_CHILD_AUTOMATION_TOKEN=<生成的随机服务令牌>
# 可选：额外保护的成员邮箱，逗号分隔。管理员和所有者默认不可编辑、移除或替换；
# 若上游页面偶尔把管理员显示为普通成员，可在此固定保护该邮箱。
TEAM_CHILD_PROTECTED_MEMBER_EMAILS=
```

`TEAM_CHILD_AUTOMATION_TOKEN` 只用于 XIASS 与成员自动化服务之间的内网请求认证。留空时成员模块会被拒绝调用；不要把真实值提交到仓库、截图或网页。

然后启动 XIASS 与显式的浏览器 profile：

```bash
cd /opt/xiass-api/deploy
docker compose -f docker-compose.local.yml --profile team-browser up -d --force-recreate xiass-api team-child-browser team-child-automation
```

这会启动名为 `xiass-api-team-child-browser` 和 `xiass-api-team-child-automation` 的容器。它们没有公开宿主机端口；XIASS 为管理员签发一次性 iframe 链接，并使用 HttpOnly 会话 Cookie 代理到 Docker 内部的 `https://team-child-browser:3001`。当前 Chromium 镜像将 Chrome DevTools 绑定在容器回环地址，自动化服务因此与浏览器共享网络命名空间，并通过 `127.0.0.1:9222` 连接同一个持久化浏览器会话；XIASS 通过 `team-child-browser:8090` 访问该自动化服务。浏览器个人资料位于 Chromium 的 `/config` 持久化挂载：本地目录 Compose 对应 `deploy/team_child_browser_data`，命名卷 Compose 对应 `team_child_browser_data`。只要不删除该目录或卷，容器重启、XIASS 重建或镜像升级都不会清除浏览器登录状态。

登录完成后，`创建 Team 子号` 默认显示成员自动化工作区，而不会自动嵌入图形浏览器。需要处理外部登录、CAPTCHA、短信、身份或工作区确认时，再点击“手动接管浏览器”。该图形界面是共享资源，`TEAM_CHILD_BROWSER_CONTROL_TTL_SECONDS`（默认 `120`）控制单个设备的短租约；第二个设备会看到明确的冲突提示，只有在 XIASS 站内确认后才会接管，避免 Chromium 主客户端被静默中断。

确认服务状态：

```bash
cd /opt/xiass-api/deploy
docker compose -f docker-compose.local.yml --profile team-browser ps
docker compose -f docker-compose.local.yml --profile team-browser logs --tail 150 xiass-api team-child-browser
```

登录后，右侧浏览器会默认打开 `https://chatgpt.com/admin/members`。首次登录或遇到 CAPTCHA、短信、身份验证时，直接在浏览器工作区完成；点击刷新并成功读取成员后，页面会自动切换到模块化“成员管理”。模块中可以邀请成员、编辑角色和从工作空间移除成员，每次成功操作都会弹出提示。需要回到可见浏览器时点击“浏览器工作区”。

若模块提示自动化服务不可用，请查看：

```bash
docker compose -f docker-compose.local.yml --profile team-browser logs --tail 150 team-child-automation team-child-browser
```

预期两个容器均为 `running`。首次打开页面时，右侧“服务器浏览器”状态会从“待连接”变为“已连接”。

## 5. 管理端使用流程

1. 登录 XIASS 管理后台，进入 `账号管理`，点击与“添加账号”相同高亮样式的 `创建 Team 子号`。
2. 确认左侧按钮显示“开始创建”，右侧“服务器浏览器”显示“已连接”。若页面提示“邮箱服务未配置”或“服务器浏览器尚未启用”，先返回第 3、4 步。
3. 点击“开始创建”。页面会生成一个临时邮箱、创建当前会话的 OpenAI 授权链接，并每 4 秒轮询一次邮箱验证码。
4. 在右侧浏览器中完成需要的管理员操作。`邀请成员邮箱` 栏会同步显示当前临时邮箱；点击复制图标即可粘贴使用。
5. 把左侧“授权链接”复制到右侧服务器浏览器地址栏，在可见浏览器中完成外部页面提示。验证码到达后，左侧会自动显示识别出的验证码。
6. 外部页面最终跳转后，复制完整回调地址（必须包含 `code` 和 `state`）到“完整回调 URL”。
7. 选择已有 OpenAI 分组，保持固定并发数 `10`、优先级 `1`，点击“校验并导入”。成功后可直接前往“账号管理”确认账号。

临时邮箱会话有效期为 20 分钟。导入成功或离开当前流程时，XIASS 会清除它在内存中的访问句柄；邮箱服务的凭据和邮箱 JWT 不会显示在管理页。

## 6. 常见问题

### 页面显示“邮箱服务未配置”

在 `账号管理` -> `创建 Team 子号` 中重新执行 `导入邮箱配置`。确认当前管理员拥有权限、文件未超过 256 KB，且 XIASS 的 `data` 目录可写。导入成功后会立即生效；如仍失败，再查看 `xiass-api` 日志和邮箱服务自身日志。

### 页面显示“服务器浏览器尚未启用”

确认 `.env` 中 `TEAM_CHILD_BROWSER_ENABLED=true`，并使用带 `--profile team-browser` 的启动命令。仅运行普通 `docker compose up -d` 不会启动这个 profile。

### 页面显示“浏览器工作区不可用”或 iframe 空白

按顺序检查：

1. `team-child-browser` 容器是否为 `running`；
2. `TEAM_CHILD_BROWSER_UPSTREAM_URL` 是否仍为 `https://team-child-browser:3001`；
3. XIASS 的反向代理是否完整保留 `/api/v1/team-child-browser/` 路径；
4. 没有给 Chromium 单独加公开端口或覆盖它的 `SUBFOLDER` 设置。

### 每次都要求重新登录

检查 `deploy/team_child_browser_data` 是否可写且在升级/清理脚本中被保留。不要删除该目录，除非你明确希望放弃当前服务器浏览器的登录状态；删除前先在维护窗口完成备份。

### 浏览器服务安全边界

该 Chromium 配置在同一 XIASS 实例的管理员之间共享，不是按管理员隔离。只有可信管理员应能访问 XIASS 管理后台。不要把 Chromium 容器的端口暴露到公网，也不要将 `.env` 或迁移出的邮箱配置文件上传到代码仓库、工单或聊天记录。
