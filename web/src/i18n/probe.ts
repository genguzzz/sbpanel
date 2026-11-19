import { dict } from '.'

export const P = dict({
  ok: 'Reachable', bad: 'Unreachable', partial: 'Some addresses unreachable', unknown: 'Not tested yet',
  quic: 'QUIC handshake', tcp: 'TCP connect', tls: 'TLS handshake', reality: 'REALITY handshake', handshake: 'Handshake + tunnel', proxy: 'Proxy request',
  notRun: 'Not run', stages: 'Test stages', pass: 'Working', fail: 'Failed', tech: 'View detailed log',
  drawerLabel: 'Connectivity test results', retest: 'Test again',
  intro: 'The panel connects to each address in the subscription the way a real client would, then makes a real request through the inbound. Test traffic isn’t counted toward any user.',
  noResults: 'No results yet', noResultsHint: 'Click “Test again” to start',
  okOf: (ok: number, n: number) => `${ok} / ${n} address${n === 1 ? '' : 'es'} working`, retesting: ' · Testing again…',
}, {
  ok: '连通正常', bad: '不可达', partial: '部分地址不可达', unknown: '还没测试过',
  quic: 'QUIC 握手', tcp: 'TCP 连接', tls: 'TLS 握手', reality: 'REALITY 握手', handshake: '握手 + 隧道', proxy: '代理请求',
  notRun: '未执行', stages: '测试阶段', pass: '可用', fail: '失败', tech: '查看详细日志',
  drawerLabel: '连通性测试结果', retest: '重新测试',
  intro: '面板会像真实客户端一样逐个连接订阅里的地址，并通过入站发起一次真实请求。测试流量不计入任何用户。',
  noResults: '还没有结果', noResultsHint: '点「重新测试」开始',
  okOf: (ok, n) => `${ok} / ${n} 个地址可用`, retesting: ' · 正在重新测试…',
})
