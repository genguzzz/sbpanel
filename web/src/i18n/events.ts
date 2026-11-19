import { dict } from '.'

export const D = dict({
  node_enrolled: 'Node enrolled', node_online: 'Node online', node_offline: 'Node offline', apply_failed: 'Deploy failed',
  quota_exceeded: 'Over quota', limit_exceeded: 'Line limit used up', user_expired: 'Expired', users_expired: 'Expired', user_reactivated: 'Reactivated',
  node_quota: 'Node quota', device_limit: 'Device limit', usage_reset: 'Usage reset', cert_renewed: 'Cert renewed',
  cert_renew_failed: 'Renewal failed', cert_expiring: 'Cert expiring', inbound_unreachable: 'Inbound down', inbound_recovered: 'Inbound up',
  update_available: 'New version', panel_upgraded: 'Panel upgraded', panel_upgrade_failed: 'Panel upgrade failed', node_upgraded: 'Node upgraded', node_upgrade_failed: 'Node upgrade failed',
  title: 'Events & audit',
  subtitle: 'System events (nodes going on/offline, failed deploys, quotas, expiries) and the admin activity log',
  events: 'System events', audit: 'Audit log',
  noRecords: 'No records yet', noEvents: 'No events yet',
  time: 'Time', admin: 'Admin', action: 'Action', target: 'Target', ip: 'Source IP',
}, {
  node_enrolled: '节点接入', node_online: '节点上线', node_offline: '节点离线', apply_failed: '配置下发失败',
  quota_exceeded: '超额', limit_exceeded: '线路限额用尽', user_expired: '到期', users_expired: '到期', user_reactivated: '恢复正常',
  node_quota: '节点流量', device_limit: '设备超限', usage_reset: '用量重置', cert_renewed: '证书续期',
  cert_renew_failed: '续期失败', cert_expiring: '证书将过期', inbound_unreachable: '入站不可达', inbound_recovered: '入站恢复',
  update_available: '新版本', panel_upgraded: '面板升级', panel_upgrade_failed: '面板升级失败', node_upgraded: '节点升级', node_upgrade_failed: '节点升级失败',
  title: '事件与审计',
  subtitle: '系统事件（节点上下线、下发失败、超额、到期）和管理员操作记录',
  events: '系统事件', audit: '操作审计',
  noRecords: '暂无记录', noEvents: '暂无事件',
  time: '时间', admin: '操作人', action: '操作', target: '对象', ip: '来源 IP',
})
