# XIASS 多节点执行与入口负载均衡

XIASS 多节点模式让多个对等应用实例共同连接独立的高可用 PostgreSQL 和 Redis 状态层。客户可继续使用同一个 Base URL 和 API Key；任一 XIASS 应用节点都能完成鉴权、调度、扣费和记录写入，余额、分组、并发、粘性、计费与使用记录始终以同一份一致状态为准。

## 链路

```text
客户 -> 高可用健康检查入口 -> 任一对等 XIASS 实例 -> 共享账号调度 -> 账号固定私网出口 -> 上游
```

- 入口负载均衡负责分担公网连接和响应带宽。
- XIASS 节点权重只决定新会话在当前最高账号优先级内使用哪个账号归属节点；它不替代公网入口的 origin 权重。
- `api2` 没有本地账号时仍可作为健康入口，应用会从共享账号池选择 `api` 节点账号；账号仍通过其绑定的 `api` 出口执行。入口健康检查不会因为本节点账号数为 0 而摘除该节点。
- 账号优先级仍是硬顺序；低优先级账号不能凭节点权重越级。
- 普通粘性会话和不可迁移的 `previous_response_id` 链继续使用原账号。
- 节点账号权重设为 `0` 后，该节点归属账号不再接收新会话，但不会中断已有粘性会话；该应用实例仍可作为健康入口，把新请求调度到其他正权重节点的账号。
- 每个账号持久保存归属节点和代理 ID，因此正常运行时 HTTP、SSE、WebSocket、图片、OAuth 刷新、额度查询和后台探测都使用相同出口。即使客户请求由另一台 XIASS 实例接入，也只会使用该账号绑定的出口。
- 每个应用实例向共享 Redis 写入 20 秒短租约心跳。只有账号归属节点心跳过期，并且 `emergency_local_egress=true` 时，存活网关才会在当前请求内临时使用本机节点出口；数据库中的账号归属和代理 ID、Redis 调度快照、凭据、计费与使用记录都不改变。归属节点心跳恢复后，新请求自动恢复原出口。
- PostgreSQL 与 Redis 不得只运行在 `api` 或 `api2` 任一应用机上，否则那台机器断联时两个网关都会失去统一鉴权和扣费能力。生产容灾必须使用托管 HA 服务，或独立部署带自动故障转移的 PostgreSQL/Redis 集群，并向两个 XIASS 节点提供同一个稳定连接地址。

## 前置条件

1. 所有节点必须运行同一个 XIASS 版本。
2. 所有节点必须连接同一套高可用 PostgreSQL 与 Redis；JWT、TOTP 加密密钥和其他应用密钥也必须一致。状态服务必须独立于任一 XIASS 应用节点的生命周期。
3. 节点之间使用 WireGuard 等低延迟私网互通，不要把 PostgreSQL、Redis 或私网 SOCKS 暴露到公网。
4. 每个执行节点在共享数据库中有一个仅私网可达的 SOCKS5 代理记录；代理记录不能被两个节点共用。
5. 先完成隔离测试，再接入带健康检查的 DNS 或边缘负载均衡。应用内账号权重不能代替公网入口负载均衡。

## 对等节点与任务领导者

两个 XIASS 实例在 HTTP 能力上完全对等：

- 数据面：`/v1`、`/v1beta`、Responses、Codex、图片和视频等模型调用路径，按入口权重在多个健康 XIASS 实例间负载均衡。
- 网页与管理端：首页、用户中心、管理 API、Team、OAuth 和上传可由任一健康 XIASS 实例提供。入口使用带健康回退的粘性 Cookie，让同一浏览器流程尽量留在一个实例；该实例断联后自动切换到另一个健康实例。
- `control_plane` 只决定哪一个实例启动无需重复运行的后台周期任务，不限制该实例可接收的 HTTP 路由，也不代表主站、唯一管理端或唯一计费节点。客户鉴权、余额扣减、使用记录、请求时 Token 刷新和调度缓存同步仍由所有网关实例通过共享状态层共同完成。
- 账号/代理到期扫描、定时测试、定时备份、渠道监控、主动 Token/额度维护等周期任务只在 `control_plane=true` 的实例运行。备份计划和渠道监控从任一对等管理面保存后，控制面会在 5 秒内从共享数据库对账，因此不要求管理员固定登录某一个域名。

数据面入口选中哪个应用实例，与最终选中哪个账号是两件独立的事。应用实例从共享账号池调度后，仍按账号持久化的节点归属和私网 SOCKS 走固定出口。因此 `api2` 接收的请求可以使用 `api` 账号，但该账号仍从 `api` 公网出口访问上游；反向亦然。

## 公网入口

应用内的“多节点执行负载均衡”只负责从共享账号池选择执行账号，并验证账号与固定出口的绑定；它不会改变客户请求到达哪一台服务器，也不会自动修改公网域名的 DNS。要分担客户主域名的公网连接和响应带宽，需要在已有的边缘入口上配置两个 HTTPS origin：

- `api` origin：现有主节点服务器的 443 端口。
- `api2` origin：新节点的 443 端口，证书必须覆盖客户访问的主域名和第二个管理/访问域名。
- 健康检查路径：`/readyz`，期望 HTTP 200；不要使用只表示进程存活的 `/health`。共享调度开启后，本节点没有账号或本节点账号权重为 `0` 仍可作为入口；只有 PostgreSQL、Redis、节点映射或整个正权重执行池均不可用时才返回 503。
- 健康检查请求应携带客户实际使用的 Host，并且两个 origin 必须都能在该 Host 下返回有效证书和响应。
- 首次接入先将 `api2` 设为很小的入口权重，确认响应、SSE、WebSocket 和图片链路后再逐步增加。

若使用支持按路径路由和主动健康检查的边缘负载均衡，客户主域名与第二访问域名都使用同一组健康 origin，因此两个地址都可使用同一 Key 并得到相同的鉴权、计费和调度结果。数据面使用可调权重；网页和管理流程使用健康感知的粘性分配，不固定到某个所谓主 origin。公网 origin 权重与 XIASS 节点账号权重相互独立：前者分担客户连接和响应带宽，后者决定新会话使用哪个节点归属账号。若暂时不能使用边缘入口，不建议用普通 DNS 轮询代替，DNS 缓存不能及时摘除故障节点。

使用外部 Caddy 时可参考 `deploy/Caddyfile.multinode.example`。示例对完整模型调用路径做 `5:1` 数据面分流，对网页和管理流程做双节点粘性分配与健康回退。Caddy 的权重只负责公网连接，XIASS 管理端的节点账号权重只负责共享账号池中的新账号选择；两者不要求相同。SSE、WebSocket 等长连接一旦建立，不会被中途搬迁。

入口层应只做一层公网转发；每个 XIASS 节点本地的 Caddy 继续直接反代本机 `127.0.0.1:8080`，不再在两个 XIASS 节点之间叠加入口代理。跨节点只允许 XIASS 为固定账号出口访问一次私网 SOCKS。

## 节点配置

所有对等 XIASS 节点都可使用 `docker-compose.standalone.yml` 连接外部 HA 状态层。每个实例的本地 `.env` 设置不同的节点 ID 和默认出口代理 ID；历史账号归属与历史出口在所有实例上必须填写相同值。

节点 `api` 示例：

```dotenv
GATEWAY_EXECUTION_NODE_ENABLED=true
GATEWAY_EXECUTION_NODE_ID=api
GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID=<api 私网 SOCKS 在共享数据库中的代理 ID>
GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS=true
GATEWAY_EXECUTION_NODE_CONTROL_PLANE=true
GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_NODE_ID=api
GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_PROXY_ID=<api 私网 SOCKS 在共享数据库中的代理 ID>
```

节点 `api2` 示例：

```dotenv
GATEWAY_EXECUTION_NODE_ENABLED=true
GATEWAY_EXECUTION_NODE_ID=api2
GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID=<api2 私网 SOCKS 在共享数据库中的代理 ID>
GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS=true
GATEWAY_EXECUTION_NODE_CONTROL_PLANE=false
GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_NODE_ID=api
GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_PROXY_ID=<api 私网 SOCKS 在共享数据库中的代理 ID>
```

配置文件也支持同名字段：

```yaml
gateway:
  execution_node:
    enabled: true
    id: api2
    default_proxy_id: 2
    emergency_local_egress: true
    control_plane: false
    legacy_unassigned_node_id: api
    legacy_unassigned_proxy_id: 1
```

## 安全启用顺序

1. 备份共享数据库、Redis、`.env` 和应用数据。
2. 在隔离端口启动每个新实例，分别检查 `/health` 和 `/readyz`，不要接入客户流量。公网入口的健康检查必须使用 `/readyz`。
3. 从每个实例分别测试其本地私网 SOCKS 和对端私网 SOCKS，确认公网出口 IP 符合账号归属。
4. 确认所有实例的数据库、Redis、密钥、版本和时区一致。
5. 全集群只允许一个实例设置 `control_plane=true`，用于启动主动 Token/额度刷新、账号与代理到期扫描、批量图片清理、定时测试、定时备份、渠道监控等不应重复运行的周期任务；其余网关副本设为 `false`。这不会影响任何节点接收客户 API、网页、管理端、Team、OAuth 或请求时 Token 刷新。该任务节点故障时，客户实时请求仍由其他节点服务；修复期间只是周期维护暂停，不会产生第二套账务状态。
6. 在“系统设置 -> 网关调度设置”填写全部节点、节点权重和不同的出口代理 ID，再开启“多节点执行负载均衡”。出口代理 ID 必须是共享数据库中的私有代理记录，主节点与第二节点的本地 `default_proxy_id` 必须分别与映射一致。
7. 首次开启会幂等迁移历史未归属账号：归属到历史节点，并且只给原本没有代理的账号补历史出口代理。存在无代理账号、未知节点、账号代理与节点映射不一致时会拒绝开启。
8. 使用独立测试用户、测试 Key 和测试账号验证非流式、SSE、WebSocket、图片、失败切换、粘性、固定出口和单次计费；禁止镜像生产客户请求。先验证两节点健康时账号只走归属出口，再停止归属节点并等待心跳租约过期，验证存活节点可用本机出口临时执行同一账号且只扣费一次。
9. 最后才把新实例加入公网健康检查入口，先使用小权重观察，再逐步增加。

## 回退

- 先在入口负载均衡中把故障应用实例的 origin 权重设为 `0` 或摘除，不需要中断其他实例。
- 应用内把对应节点账号权重设为 `0`，停止新会话进入该节点归属账号；这不会自动摘除该应用入口。
- 不删除共享数据库、Redis、账号代理或数据卷。
- 修复后先在隔离端口验证，再恢复小权重。

多节点状态共享时禁止同时运行不同数据库迁移版本，也禁止使用 `docker compose down -v`。

## 容灾边界

- 单个 XIASS 应用节点断联：入口在健康检查失败后摘除该 origin，新请求进入另一节点；共享心跳过期后，存活节点可在不改变账号归属和账务状态的前提下，临时用本机出口执行故障节点归属账号。
- 单个账号出口断联：只影响当前请求并触发账号故障转移，不会因一次网络波动把账号持久停调；网络恢复后下一次请求可以再次选择它。
- HA PostgreSQL 或 Redis 短暂切换：`/readyz` 在统一状态不可用时返回 503，入口不再把新请求送入未就绪实例；状态层恢复后实例自动重新加入。
- 公网入口本身断联：单台 Caddy 不能解决自身故障。生产环境需使用托管全局负载均衡，或至少两个独立入口共同指向同一组 XIASS origins。
- 已经送达上游的流式或 POST 请求不会在另一个应用节点透明重放。这样可避免一次请求被上游执行两次并重复产生费用；客户端重新发起的请求继续走正常鉴权、幂等和计费链路。
