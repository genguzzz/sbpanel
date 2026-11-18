# sbpanel

[English](README.md) | **简体中文**

基于 **sing-box** 的代理管理面板：master 与 node 分离部署，按连接精确计数，自带流量分析（不再需要单独部署 Neko Master）。

![概览](docs/img/overview.png)

<table>
  <tr>
    <td><img src="docs/img/users.png" alt="用户详情抽屉"></td>
    <td><img src="docs/img/traffic-dark.png" alt="流量分析（深色）"></td>
  </tr>
  <tr>
    <td align="center">用户：额度、到期、设备、单用户流量</td>
    <td align="center">流量分析：细到 用户 × 域名 × 来源 IP</td>
  </tr>
  <tr>
    <td><img src="docs/img/routing.png" alt="分流规则编辑"></td>
    <td><img src="docs/img/subpage.png" alt="可定制主题的订阅页"></td>
  </tr>
  <tr>
    <td align="center">一套分流规则，生成 Clash / sing-box / Surge 配置</td>
    <td align="center">给用户看的订阅页，支持主题定制</td>
  </tr>
  <tr>
    <td><img src="docs/img/certs.png" alt="域名与证书"></td>
    <td><img src="docs/img/settings-api.png" alt="API 密钥与 MCP"></td>
  </tr>
  <tr>
    <td align="center">域名、证书与连通性测试</td>
    <td align="center">带权限的 API 密钥与内置 MCP 服务器</td>
  </tr>
</table>

设计文档见 [docs/DESIGN.md](docs/DESIGN.md)。

## 功能

| 模块 | 能力 |
|---|---|
| **节点** | 一行命令接入（一次性令牌 + CSR，面板自签 CA 签发证书，mTLS / TLS 1.3）；节点主动连接 master，可以在 NAT 后面；证书到期前自动续签，删除节点即吊销；显示 CPU、内存、实时速率、月流量配额（80% / 100% 告警） |
| **内核** | sing-box 1.14 以 Go 库方式内嵌；只改用户时**原地热更新**（vless / vmess / trojan / shadowsocks），已有连接不断；其它入站改动只重建该入站；骨架改动先校验再蓝绿切换，配置有误时旧配置继续服务 |
| **入站** | VLESS（REALITY / Vision、WS、gRPC、HTTPUpgrade）、VMess、Trojan、Shadowsocks 2022、Hysteria2、TUIC v5、AnyTLS；一键生成 REALITY 密钥对；支持 PROXY protocol，可放在 nginx 443 SNI 分流后面 |
| **用户** | 流量额度、到期时间、重置周期（日 / 周 / 月 / 按创建日滚动）、**限速**（同一用户所有连接共享）、**设备数**（按来源 IP，节点本地拒绝 + master 跨节点校正）、批量操作可撤销、更换密钥、踢下线 |
| **流量限额** | 限额预设：对一组入站额外限量，例如对通往家宽的所有中转设“每月 50 GB”，按用户应用（用户表单、批量、或在预设里添加），可为单个用户自定义大小；未应用的用户不受限制。超额后只失去这些入站（断开其上的连接、订阅中隐藏），按用户自己的周期或每月固定日期重置。用户详情和订阅页都能看到各入站的用量 |
| **分组** | 用户 ↔ 入站多对多；默认分组自动接纳新用户和新入站 |
| **订阅** | `/s/<令牌>`（3x-ui 的 `/sub/<subId>` 继续可用）；按 User-Agent 返回 sing-box JSON、Clash/mihomo YAML、Surge 或 base64；浏览器打开是可定制主题的自助页（用量、二维码、一键导入）；Host 层可覆盖地址、端口、SNI、Host、路径（CDN、中转、多域名）；订阅访问日志 |
| **分流配置** | 规则和策略组只定义一次，按客户端生成 Clash/mihomo、sing-box、Surge 配置，客户端特有字段用占位模板；可视化编辑 + 高级 JSON，发布前预览和对比；内置 标准分流 / 全局代理 / 绕过大陆 / 极简 |
| **流量分析** | 精确到 用户 × 节点 × 入站 × 出站 × 域名 × 来源 IP × 端口 × 规则；最近 30 天内任意区间，精确到分钟；粒度 1 分钟 / 5 分钟 / 15 分钟 / 1 小时 / 1 天；实时连接列表，可断开 |
| **域名与证书** | 引导式设置：填入 Cloudflare API Token 后自动建 DNS 记录、用 DNS-01 签发证书（支持通配符，节点不用开 80 端口）；也可以导入 PEM，或直接用节点上 certbot 的证书；续期只替换证书文件，不重建入站 |
| **连通性测试** | master 用隐藏的探测用户逐段测试每个入站（DNS → TCP/QUIC → TLS → 代理），并指出是哪一侧的问题：端口未开放、SNI 不匹配、入站拒绝了客户端，还是入站后面的上游不可达 |
| **API 与智能体** | 带权限范围的 API 密钥（read / users:write / nodes:write / settings:write / admin），可设有效期、来源 IP 白名单、限流；OpenAPI 3.1 文档与面板内文档页；内置 MCP 服务器（`POST /mcp`），Claude Code 等智能体可直接查询和管理用户 |
| **版本更新** | 面板定期检查 GitHub 发布，有新版本时在顶栏和通知里提示。一键升级面板或任意节点（节点从面板获取校验过的程序，无需访问 GitHub）。旧程序保留为 `.prev`；新版本没有报告健康（或启动后很快崩溃）时自动换回并重启。也可在同一页面手动回退，或在服务器上执行 `sbmaster upgrade` / `sbnode upgrade [--version vX.Y.Z] [--rollback]`。 |
| **运维** | 两步验证（TOTP）、审计日志、Telegram / Webhook 通知、一键备份、`sbmaster reset-admin`、从 3x-ui 迁移（API JSON 或 `x-ui.db`） |
| **界面** | React 19、react-aria-components、Tailwind v4、ECharts；四套配色（Graphite、Rosé Pine、Catppuccin、Nord）各有浅色和深色，可自定义强调色；⌘K 命令面板，`g`+字母 跳转；手机布局 |

### 流量不丢也不重复
节点为每条连接计数，每 10 秒汇总成报告，**先写入本地 bbolt 再发送**；master 在同一个事务里按 `(节点, seq)` 去重并累加，确认后节点才删除。master 宕机期间报告在节点上排队（超过上限时合并成更粗的时间窗，不直接丢弃），重发不会重复计入。

## 一键安装

支持 Debian / Ubuntu、RHEL / CentOS 7+ / Rocky / Alma / Fedora、Alpine（OpenRC）、Arch、openSUSE，架构 amd64 / arm64。安装包会做 SHA256 校验。

**面板（master）**
```bash
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- master \
  --web 127.0.0.1:8080 --grpc :9443 --public-grpc panel.example.com:9443
```
安装完成后会打印初始管理员密码。Web 默认只监听本机，请用 nginx 或 Caddy 反代并配置 HTTPS；节点通过 gRPC 端口（默认 9443）连接 master，需要在防火墙放行。

**节点**：在面板「节点 → 添加节点」中复制命令，格式如下
```bash
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- node \
  --master panel.example.com:9443 --token <一次性令牌> --ca-sha256 <CA 指纹>
```

**升级 / 卸载 / 状态**
```bash
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- upgrade
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- uninstall node --purge
curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- status
```
GitHub 访问不畅时，可以加 `--mirror https://<你的 GitHub 代理>/` 走镜像；用 `--version vX.Y.Z` 安装指定版本。

从 v0.4.0 起也可以在面板（设置 → 版本更新）或服务器上直接升级，带健康检查和失败自动回退：
```bash
sbmaster upgrade                 # 最新版本；可加 --version vX.Y.Z、--rollback、--mirror URL
sbnode upgrade
```

**Docker**：`ghcr.io/genguzzz/sbpanel`（amd64 / arm64）是一个不含 sbpanel 程序的小型运行环境镜像。每次启动时按 `SBP_VERSION`（默认 `latest`，也可写 `v0.4.0` 这样的版本）下载发布包，用 `checksums.txt` 校验后缓存在数据卷里，所以 GitHub 不通时容器也能启动。`SBP_MIRROR=https://<你的 GitHub 代理>/` 可让下载走代理。现成的 compose 文件见 [deploy/docker/compose.yml](deploy/docker/compose.yml)。
```bash
# 面板：Web 界面在 127.0.0.1:8080（前面加 HTTPS 反代），节点连接 9443
docker run -d --name sbmaster --restart unless-stopped -p 127.0.0.1:8080:8080 -p 9443:9443 \
  -e SBM_PUBLIC_GRPC=panel.example.com:9443 -v sbmaster:/var/lib/sbmaster ghcr.io/genguzzz/sbpanel
docker logs sbmaster | grep -i password      # 初始管理员密码（也可用 SBM_ADMIN_PASSWORD 指定）

# 节点：使用主机网络，面板分配的入站端口才能直接访问；首次启动时自动注册
docker run -d --name sbnode --restart unless-stopped --network host --cap-add NET_ADMIN \
  -e SBN_MASTER=panel.example.com:9443 -e SBN_TOKEN=<一次性令牌> -e SBN_CA_SHA256=<CA 指纹> \
  -v sbnode:/var/lib/sbnode ghcr.io/genguzzz/sbpanel node
```
重启容器即可升级（`docker restart sbmaster`）；若固定了 `SBP_VERSION`，修改后重建容器。面板会显示这条命令而不是一键升级。其他命令可直接执行，如 `docker exec -it sbmaster sbmaster reset-admin --user admin --password -`。

**找回管理员账号**
```bash
sbmaster reset-admin --user admin --password -                  # 从标准输入读取新密码
sbmaster reset-admin --user alice --rename admin --password -   # 同时改用户名
```

**从 3x-ui 迁移**
- 只迁移用户：`sbmaster import-3xui --users-only --json export.json`。用户名、UUID、密码、额度、到期时间、subId 都保持不变，原来的 `/sub/<subId>` 订阅链接继续可用。
- 连同入站一起迁移：`--node <ID> --db /etc/x-ui/x-ui.db`。
- 两种方式都可以先加 `--dry-run` 预览。

## API 与 AI 智能体

在「设置 → API 与智能体」创建密钥（只显示一次），按需勾选权限：

```bash
curl -H "Authorization: Bearer sbp_…" https://panel.example.com/api/overview
claude mcp add --transport http sbpanel https://panel.example.com/mcp --header "Authorization: Bearer sbp_…"
```

接口描述在 `GET /api/openapi.json`（面板内「API 文档」页可浏览）。权限、限流、MCP 工具和其它客户端的配置见 [docs/API.md](docs/API.md)；证书见 [docs/CERTS.md](docs/CERTS.md)；订阅页主题见 [docs/SUB-THEMES.md](docs/SUB-THEMES.md)。

## 构建与测试

```bash
make web          # 前端（pnpm）
make master node  # 本机二进制
make linux        # linux amd64/arm64 静态二进制（CGO=0）
make test         # 单元测试 + 端到端测试
make test-race    # 加上 race 检测
```

需要 Go 1.25+（go.mod 会自动拉取工具链）和 pnpm。

端到端测试使用真实 sing-box 客户端，配置取自用户自己的订阅，覆盖：
- 各种入站组合逐一传输并核对计费；
- 经 REALITY 传输 100 MiB，master 与节点计数逐字节一致；
- master 宕机后补传，不丢失也不重复；
- 热更新时其它用户的连接不断；
- 限速、设备数、到期断线与续期恢复；
- 模板发布，配置错误时自动保留旧配置；
- 节点证书吊销；
- 连通性测试，包括上游不可达的情况；
- API 密钥（权限、过期、IP 白名单、限流）与 MCP 端点；
- 3x-ui 迁移后旧客户端和旧订阅链接继续可用。

## 本地演示

```bash
make web master node && go build -tags with_quic,with_utls -ldflags=-checklinkname=0 -o bin/devclient ./tools/devclient
scripts/dev-demo.sh start     # master + node + 用户 + VLESS REALITY 入站 + 两个客户端
scripts/dev-demo.sh traffic   # 通过两个用户访问一批网站
scripts/dev-demo.sh stop
```

上面的截图都来自这个本地演示。

## 许可

sbpanel 以库的形式链接 sing-box（GPL-3.0-or-later），并在 `internal/node/hot/` 中包含修改过的 sing-box 源码，因此**分发二进制时整个项目须遵守 GPL-3.0-or-later**。
