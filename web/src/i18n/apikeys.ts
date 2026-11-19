import { createElement as h, Fragment, type ReactNode } from 'react'
import { dict } from '.'

/** API key scopes and MCP tool descriptions (lib/apikeys.ts). */
export const K = dict({
  s_read: 'Read only', s_read_d: 'Read users, nodes, inbounds, traffic, events and settings (no key management or backups)',
  s_users: 'Write users', s_users_d: 'Create, edit and delete users; extend, add traffic, reset usage, batch actions, disconnect',
  s_nodes: 'Write nodes', s_nodes_d: 'Nodes, inbounds, groups, hosts, route templates and subscription routing profiles',
  s_settings: 'Write settings', s_settings_d: 'Panel settings, appearance, subscription page themes, notification channels',
  s_admin: 'Admin', s_admin_d: 'Everything, including managing API keys, downloading backups and 3x-ui import',
  list_users: 'List / search users', get_user: 'User details and subscription link', traffic_summary: 'Traffic totals by user / node / inbound / domain',
  top_domains: 'Top domains', list_nodes: 'Node health', list_inbounds: 'List inbounds', list_events: 'Events and audit log',
  get_live_connections: 'Online users and live connections', create_user: 'Create a user', update_user: 'Edit a user', extend_user: 'Extend expiry',
  add_traffic: 'Add traffic', set_user_enabled: 'Enable / disable', reset_usage: 'Reset usage', kick_user: 'Disconnect a user',
}, {
  s_read: '只读', s_read_d: '读取用户、节点、入站、流量、事件和设置（不含密钥管理与备份）',
  s_users: '用户写入', s_users_d: '创建、修改、删除用户，续期、加流量、重置用量、批量操作、断开连接',
  s_nodes: '节点写入', s_nodes_d: '节点、入站、分组、Host、路由模板与订阅分流配置',
  s_settings: '设置写入', s_settings_d: '面板设置、外观、订阅页主题、通知渠道',
  s_admin: '管理员', s_admin_d: '全部权限，包括管理 API 密钥、下载备份和 3x-ui 导入',
  list_users: '列出/搜索用户', get_user: '用户详情与订阅链接', traffic_summary: '按用户/节点/入站/域名汇总流量',
  top_domains: '热门域名', list_nodes: '节点健康', list_inbounds: '入站列表', list_events: '事件与审计',
  get_live_connections: '在线用户与实时连接', create_user: '创建用户', update_user: '修改用户', extend_user: '续期',
  add_traffic: '加流量', set_user_enabled: '启用/禁用', reset_usage: '重置用量', kick_user: '断开用户连接',
})

const frag = (...c: ReactNode[]) => h(Fragment, null, ...c)
const mono = (s: string) => h('span', { className: 'font-mono' }, s)
const b = (s: string) => h('b', { className: 'text-text' }, s)
const link = () => h('a', { className: 'text-accent underline-offset-2 hover:underline', href: 'https://modelcontextprotocol.io', target: '_blank', rel: 'noreferrer' }, 'Model Context Protocol')

/** Settings → API & agents (pages/settings/ApiAgents.tsx). */
export const AG = dict({
  expired: 'Expired', neverExpires: 'Never expires', expiresOn: (d: string) => `Expires ${d}`, keyDeleted: 'Key deleted',
  apiKeys: 'API keys', createKey: 'Create key',
  intro: () => frag('Scripts, bots and AI agents call ', mono('/api/*'), ' and ', mono('/mcp'), ' with ', mono('Authorization: Bearer sbp_…'),
    '. Each key has only the scopes you choose, plus optional expiry, source IPs and rate limit; its actions are recorded in the audit log as ', mono('token:NAME'), '.'),
  empty: 'No API keys yet. Create a read-only key for monitoring, or give an agent a “Read only + Write users” key.',
  validity: 'Expiry', rate: 'Rate', perMin: (n: number) => `${n}/min`, source: 'Source', anyIp: 'Any', lastUsed: 'Last used',
  deleteKeyX: (n: string) => `Delete key ${n}`, deleteKey: 'Delete key',
  deleteBody: (n: string) => frag('Scripts and agents using ', b(n), ' lose access immediately. This can’t be undone.'),
  e_never: 'Never', e_7: '7 days', e_30: '30 days', e_90: '90 days', e_365: '1 year',
  created: 'Key created', once: 'This is the only time the full key is shown. Copy it now and keep it safe; if you lose it, delete it and create a new one.',
  testCurl: 'Test with curl', addClaude: 'Add the MCP server in Claude Code', otherMcp: 'Other MCP clients (JSON config)',
  createTitle: 'Create API key', name: 'Name', namePh: 'e.g. claude-code, grafana', nameHint: 'Shown in the audit log as token:NAME',
  desc: 'Description (optional)', descPh: 'Purpose, owner', scopes: 'Scopes', pickScope: 'Select at least one scope',
  adminWarn: 'Admin keys can create new keys and download backups. Only give them to programs you fully trust.',
  ips: 'Source IP allowlist (optional)', ipsHint: 'One IP or CIDR per line; leave empty to allow any. Behind a reverse proxy, X-Forwarded-For is trusted only from localhost.',
  rateLabel: 'Rate limit (requests/min)', rateErr: 'A whole number from 1 to 100000', rateHint: (n: number) => `Leave empty for the default ${n}/min; requests over the limit get 429.`,
  listSep: ', ',
  mcpTitle: 'MCP agents',
  mcpIntro: () => frag('The panel has a built-in ', link(), ' server (Streamable HTTP). Agents such as Claude Code, Claude Desktop and Cursor connect with an API key to query users, traffic and nodes, or — when allowed — create and extend users.'),
  endpoint: 'Endpoint', tools: 'Tools',
  toolsHint: 'Grey tools are read-only (need read); blue tools write (need users:write) and are recorded in the audit log as mcp:NAME.',
  docsTitle: 'API docs',
  docsIntro: 'A complete OpenAPI 3.1 description covers auth, users (including batch), nodes, inbounds, groups, hosts, templates, routing profiles, traffic, events and audit, and settings, with the scope each endpoint requires.',
  auth: 'Auth', spec: 'Spec', limit: 'Over limit', limitBody: () => frag('HTTP 429, with ', mono('Retry-After'), ' giving the seconds to wait'),
  openDocs: 'Open API docs', dlSpec: 'Download openapi.json',
}, {
  expired: '已过期', neverExpires: '永不过期', expiresOn: (d) => `${d} 到期`, keyDeleted: '密钥已删除',
  apiKeys: 'API 密钥', createKey: '创建密钥',
  intro: () => frag('脚本、机器人和 AI 智能体用 ', mono('Authorization: Bearer sbp_…'), ' 调用 ', mono('/api/*'), ' 和 ', mono('/mcp'),
    '。每个密钥只拥有所选权限，可设置有效期、来源 IP 和速率限制；操作会以 ', mono('token:名称'), ' 记入审计日志。'),
  empty: '还没有 API 密钥。创建一个只读密钥给监控用，或给智能体一个「只读 + 用户写入」密钥。',
  validity: '有效期', rate: '速率', perMin: (n) => `${n} 次/分`, source: '来源', anyIp: '不限', lastUsed: '最近使用',
  deleteKeyX: (n) => `删除密钥 ${n}`, deleteKey: '删除密钥',
  deleteBody: (n) => frag('使用 ', b(n), ' 的脚本和智能体会立刻失去访问权限。此操作不可撤销。'),
  e_never: '永不', e_7: '7 天', e_30: '30 天', e_90: '90 天', e_365: '1 年',
  created: '密钥已创建', once: '这是唯一一次显示完整密钥。请现在复制并妥善保存；丢失后只能删除并重新创建。',
  testCurl: '用 curl 测试', addClaude: '在 Claude Code 中添加 MCP 服务器', otherMcp: '其他 MCP 客户端（JSON 配置）',
  createTitle: '创建 API 密钥', name: '名称', namePh: '例如 claude-code、grafana', nameHint: '审计日志里显示为 token:名称',
  desc: '描述（可选）', descPh: '用途、负责人', scopes: '权限', pickScope: '至少选择一个权限',
  adminWarn: '管理员密钥可以创建新密钥和下载备份，只给完全信任的程序。',
  ips: '来源 IP 允许列表（可选）', ipsHint: '每行一个 IP 或 CIDR，留空不限。反向代理只信任来自本机的 X-Forwarded-For。',
  rateLabel: '速率限制（次/分钟）', rateErr: '1 – 100000 的整数', rateHint: (n) => `留空为默认 ${n} 次/分钟，超出返回 429。`,
  listSep: '、',
  mcpTitle: 'MCP 智能体接入',
  mcpIntro: () => frag('面板内置 ', link(), ' 服务器（Streamable HTTP）。Claude Code、Claude Desktop、Cursor 等智能体用 API 密钥连接后，可以直接查询用户、流量和节点，或在授权后开通、续期用户。'),
  endpoint: '端点', tools: '工具',
  toolsHint: '灰色为只读工具（需要 read）；蓝色为写入工具（需要 users:write），调用记入审计日志 mcp:名称。',
  docsTitle: 'API 文档',
  docsIntro: '完整的 OpenAPI 3.1 描述覆盖认证、用户（含批量）、节点、入站、分组、Host、模板、分流配置、流量、事件与审计、设置，每个接口标注所需权限。',
  auth: '认证', spec: '规范', limit: '超限', limitBody: () => frag('HTTP 429，', mono('Retry-After'), ' 给出等待秒数'),
  openDocs: '打开 API 文档', dlSpec: '下载 openapi.json',
})
