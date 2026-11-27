import { dict } from '.'

const s = (n: number) => (n === 1 ? '' : 's')

/** App shell: navigation, header, ⌘K palette, shortcuts, login. */
export const SH = dict({
  gObserve: 'Monitor', gUsers: 'Users', gNodes: 'Nodes & core', gSystem: 'System',
  overview: 'Overview', traffic: 'Traffic', live: 'Live connections', events: 'Events & audit', users: 'Users', groups: 'Groups', limits: 'Traffic limits',
  subscriptions: 'Subscriptions', nodes: 'Nodes', inbounds: 'Inbounds', certs: 'Domains & certificates', templates: 'Route templates', settings: 'Settings',
  skip: 'Skip to main content', searchBox: 'Search pages, users, nodes…', drafts: (n: number) => `${n} template draft${s(n)} to publish`,
  search: 'Search', refresh: 'Refresh data', toLight: 'Switch to light mode', toDark: 'Switch to dark mode', logout: 'Sign out',
  mainNav: 'Main navigation', bottomNav: 'Bottom navigation', more: 'More', morePages: 'More pages',
  hintSearch: 'search', hintJump: '+letter to jump', hintKeys: 'shortcuts',
  nodesBad: (n: number) => `${n} node${s(n)} unhealthy`, syncing: 'Syncing…', synced: 'Synced',
  syncedTitle: (n: number) => `${n} node${s(n)} on the latest config`, syncingTitle: (n: number) => `${n} node${s(n)} applying new config`,
  badNodes: 'Unhealthy nodes', offline: 'Offline', applyFailed: 'Deploy failed', lastSeen: (t: string) => `Last seen ${t}`, toNodes: 'Go to Nodes →',
  secRecent: 'Recent', secPage: 'Pages', secUser: 'Users', secNode: 'Nodes', secMore: 'Related pages', secAction: 'Actions',
  aNewUser: 'New user', aNewNode: 'Add node', aExpiring: 'Show users expiring soon', a2fa: 'Turn on two-factor authentication',
  aApiKey: 'Create an API key / connect an MCP agent', aApiDocs: 'API docs (OpenAPI)', aRouting: 'Edit routing profiles',
  aImport: 'Migrate from 3x-ui', aBackup: 'Download backup', viewTraffic: 'View traffic',
  copiedSub: (u: string) => `Copied ${u}’s subscription link`,
  paletteInput: 'Search pages, users, nodes, actions…', noResults: 'No results', copySub: 'copy subscription',
  kSelect: 'select', kOpen: 'open', kCopy: 'copy subscription', kClose: 'close',
  shortcuts: 'Keyboard shortcuts', scPalette: 'Command palette', scFocus: 'Focus the search box', scHelp: 'Keyboard shortcuts',
  scGo: (p: string) => `Go to ${p}`, scJK: 'User list: move up / down; detail drawer: previous / next', scEnter: 'Open user details',
  scX: 'Select the current user', scAll: 'Select all filtered results', scEsc: 'Clear selection / close dialog',
  pageError: 'Something went wrong on this page', retry: 'Retry',
  feedback: 'Report an issue', feedbackTitle: 'Report a problem or suggest a feature on GitHub', poweredBy: 'Powered by', sourceRepo: 'Source code on GitHub',
  // login
  tagline: 'sing-box proxy panel', username: 'Username', password: 'Password', totp: 'Two-factor code', totpHint: 'The 6-digit code from your authenticator app',
  signingIn: 'Signing in…', signIn: 'Sign in', or: 'or', passkeySignIn: 'Sign in with a passkey',
}, {
  gObserve: '观测', gUsers: '用户', gNodes: '节点与内核', gSystem: '系统',
  overview: '概览', traffic: '流量分析', live: '实时连接', events: '事件与审计', users: '用户', groups: '分组', limits: '流量限额',
  subscriptions: '订阅', nodes: '节点', inbounds: '入站', certs: '域名与证书', templates: '路由模板', settings: '设置',
  skip: '跳到主要内容', searchBox: '搜索页面、用户、节点…', drafts: (n) => `${n} 个模板草稿待发布`,
  search: '搜索', refresh: '刷新数据', toLight: '切换到浅色', toDark: '切换到深色', logout: '退出登录',
  mainNav: '主导航', bottomNav: '底部导航', more: '更多', morePages: '更多页面',
  hintSearch: '搜索', hintJump: '+字母 跳转', hintKeys: '快捷键',
  nodesBad: (n) => `${n} 个节点异常`, syncing: '同步中…', synced: '已同步',
  syncedTitle: (n) => `${n} 个节点配置已是最新`, syncingTitle: (n) => `${n} 个节点正在应用新配置`,
  badNodes: '异常节点', offline: '离线', applyFailed: '下发失败', lastSeen: (t) => `最后在线 ${t}`, toNodes: '前往节点页 →',
  secRecent: '最近访问', secPage: '页面', secUser: '用户', secNode: '节点', secMore: '相关页面', secAction: '操作',
  aNewUser: '新建用户', aNewNode: '添加节点', aExpiring: '查看即将到期的用户', a2fa: '开启两步验证',
  aApiKey: '创建 API 密钥 / 连接 MCP 智能体', aApiDocs: 'API 文档（OpenAPI）', aRouting: '编辑分流规则（订阅配置模板）',
  aImport: '从 3x-ui 迁移', aBackup: '下载备份', viewTraffic: '查看流量',
  copiedSub: (u) => `已复制 ${u} 的订阅链接`,
  paletteInput: '搜索页面、用户、节点、操作…', noResults: '没有结果', copySub: '复制订阅',
  kSelect: '选择', kOpen: '打开', kCopy: '复制订阅', kClose: '关闭',
  shortcuts: '键盘快捷键', scPalette: '命令面板', scFocus: '聚焦搜索框', scHelp: '快捷键帮助',
  scGo: (p) => `前往${p}`, scJK: '用户列表：上下移动；详情抽屉：上一个 / 下一个', scEnter: '打开用户详情',
  scX: '选中当前用户', scAll: '全选当前筛选结果', scEsc: '取消选择 / 关闭弹窗',
  pageError: '页面出错了', retry: '重试',
  feedback: '问题反馈', feedbackTitle: '在 GitHub 上反馈问题或提建议', poweredBy: 'Powered by', sourceRepo: '在 GitHub 上查看源码',
  tagline: 'sing-box 代理管理面板', username: '用户名', password: '密码', totp: '两步验证码', totpHint: '验证器 App 中的 6 位数字',
  signingIn: '登录中…', signIn: '登录', or: '或', passkeySignIn: '使用通行密钥登录',
})

/** Extra ⌘K search keywords per page, in the UI language (the English ids/keywords in App.tsx always match). */
export const KW = dict({
  overview: '', traffic: 'usage bandwidth', live: 'online sessions', events: 'history', users: 'accounts', groups: '', limits: 'quota cap home line',
  subscriptions: 'rules', nodes: 'servers', inbounds: 'protocols connectivity', certs: 'renew https', templates: 'rules', settings: 'notifications migrate',
}, {
  overview: '概览 首页', traffic: '流量 域名', live: '连接 在线', events: '事件 审计 日志', users: '用户 客户端', groups: '分组', limits: '限额 家宽 线路 流量限制',
  subscriptions: '订阅 分流 规则 模板', nodes: '节点 服务器', inbounds: '入站 协议 连通性', certs: '证书 域名 续期', templates: '路由 出站 规则 模板', settings: '设置 迁移 通知 备份',
})
