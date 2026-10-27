import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { SessionViewer } from '../src/components/session/SessionViewer';
import { I18nProvider } from '../src/i18n';
import { sessionService } from '../src/services/session';
let requests = 0;
sessionService.getToolCall = async (_root, _session, callId) => {
  requests++;
  return {callId, kind: 'read', title: callId, status: 'complete', content: [{type:'text',text:'REMOTE DETAIL '+callId+'\n'+('detail line\n'.repeat(80))}]};
};
const exchanges = Array.from({length: 30}, (_, i) => [
  {seq:i*2+1,role:'user',content:'User question '+i,timestamp:'2026-09-28T00:00:00Z'},
  {seq:i*2+2,role:'agent',content:'Agent answer '+i,timestamp:'2026-09-28T00:00:01Z'},
]).flat();
const exchange_aux = Object.fromEntries(Array.from({length:30},(_,i)=>[i*2+2,Array.from({length:100},(_,j)=>({seq:i*2+2,line:0,toolcall:{callId:`call-${i}-${j}`,kind:'read',status:'complete',title:`Tool ${i}-${j}`,meta:{}}}))]));

// ── 窗口分页 fixture（2026-10-27 G-KB 回归）──────────────────────────────────
// 「上翻跳变 / 到底按钮停在半路 / 按钮显隐不跟位置」三个报障都只在**窗口分页 + 虚拟化**
// 同时成立的路径上出现。真跑那条路径需要后端（`loadMore` 走 protectedJSON，fixture 里被
// bootstrap 门禁挡成 api_not_ready），所以直接替换 `sessionService.getSessionWindow`：
// 没有 HTTP、没有 agent，但走的是**同一份** useSessionWindowing 代码。
// 键只认 'paging-test'，其它会话返回 null（与改造前的失败路径等价），既有检查不受影响。
const PAGING_TOTAL = 240;
const PAGING_PAGE = 8;
const pagingExchanges = Array.from({ length: PAGING_TOTAL }, (_, i) => ({
  seq: i + 1,
  role: i % 2 === 0 ? 'user' : 'agent',
  content: (i % 2 === 0 ? `Question ${i + 1}` : `Answer ${i + 1}`) +
    '\n\n' + Array.from({ length: 8 }, (_, k) => `body line ${i}-${k}`).join('\n\n'),
  timestamp: '2026-09-28T00:00:00Z',
}));
// 真实会话的**形状**：每条 exchange 都带一叠工具卡，一页 8 条 exchange 展开成 60+ 个
// 时间线条目。这不是装饰 —— 2026-10-27「一次上滑连翻到底」只在条目数超过 overscan 时
// 出现：前插后视口锚行被推到索引 60 开外，永远不再挂载。轻量夹具（每页 16 条）前后都还
// 挂载着锚行，把这条路径整个挡住了。
// kind 交替 read/execute：连续同类 ≥ 5 会被 groupConsecutiveToolCalls 折成一张组卡，
// 条目数就塌回 16 了，夹具必须避开分组。
const PAGING_AUX: Record<string, any[]> = {};
for (let i = 0; i < PAGING_TOTAL; i++) {
  PAGING_AUX[String(i + 1)] = Array.from({ length: 20 }, (_, k) => ({
    seq: i + 1, line: 0,
    toolcall: {
      callId: `p-${i}-${k}`, kind: k % 2 ? 'read' : 'execute', status: 'complete',
      title: `Tool ${i}-${k}`, meta: { command: 'echo fixture' },
    },
  }));
}
let lastPrepend: { key: string; top: number } | null = null;
const pagingWindow = (beforeSeq?: number, latest?: number) => {
  const slice = beforeSeq && beforeSeq > 0
    ? pagingExchanges.filter(e => e.seq < beforeSeq).slice(-PAGING_PAGE)
    : pagingExchanges.slice(-(latest || PAGING_PAGE));
  if (!slice.length) return null;
  return {
    session: {
      key: 'paging-test',
      exchanges: slice,
      exchange_aux: Object.fromEntries(slice.map(e => [String(e.seq), PAGING_AUX[String(e.seq)] || []])),
      blocks: {},
    },
    meta: {
      total: PAGING_TOTAL, hasMore: slice[0].seq > 1,
      minSeq: slice[0].seq, maxSeq: slice[slice.length - 1].seq,
    },
  };
};
let pagingPrepends = 0;
sessionService.getSessionWindow = async (_root, key, opts) => {
  // 'virtual-test' 建模「后端一次给全量历史」：`useTargetSeqScroll` 的虚拟化快路径要求
  // `windowMeta.minSeq > 0 && target ∈ [minSeq, maxSeq]`，否则目标行没挂载就永远找不到。
  // 缺了这份 meta 时 `jump(21)` 必然失败，与产品行为无关。
  if (key === 'virtual-test') {
    return {
      session: { key, exchanges, exchange_aux },
      meta: { total: exchanges.length, hasMore: false, minSeq: 1, maxSeq: exchanges.length },
    } as any;
  }
  if (key !== 'paging-test') return null;
  if (opts?.beforeSeq) {
    pagingPrepends += 1;
    // 前插请求发出的那一刻记下视口首行的**真实位置** —— 回包后是否跳动由它判定。
    // 用 data-timeline-key（虚拟行包装层）而不是 data-session-seq：前插后锚行可能
    // 被卸载，包装层的键是唯一稳定的身份。
    const containerTop = [...document.querySelectorAll('div')]
      .find(node => getComputedStyle(node).overflowY === 'auto')?.getBoundingClientRect().top ?? 0;
    const rows = [...document.querySelectorAll('[data-timeline-key]')];
    const first = rows.find(row => row.getBoundingClientRect().top >= containerTop - 1);
    lastPrepend = first
      ? { key: String(first.getAttribute('data-timeline-key') || ''), top: first.getBoundingClientRect().top }
      : null;
  }
  return pagingWindow(opts?.beforeSeq, opts?.latest) as any;
};
function Check() {
  const [session,setSession]=useState<any>({key:'virtual-test',exchanges,exchange_aux});
  const [target,setTarget]=useState(0);
  const [shown,setShown]=useState(true);
  Object.assign(window,{check:{requests:()=>requests,jump:(seq:number)=>setTarget(seq),toggle:()=>setShown(x=>!x),append:()=>setSession((s:any)=>({...s,exchanges:[...s.exchanges,{seq:s.exchanges.length+1,role:'agent',content:'APPENDED '+s.exchanges.length,timestamp:'2026-09-28T00:00:02Z'}]}))}});
  Object.assign((window as any).check, {
    replaceSession: (next: any) => { setTarget(0); setSession(next); },
    paging: () => { setTarget(0); setSession({key:'paging-test', exchanges:[], exchange_aux:{}}); },
    prepends: () => pagingPrepends,
    question: () => setSession((s: any) => ({...s, pending: true, exchanges: [...s.exchanges, {
      role: 'tool', content: '', toolCall: {
        callId: 'question', kind: 'ask_user', status: 'running',
        meta: {questions: [{question: 'Draft retention check'}]},
      },
    }]})),
    short: () => setSession({key:'short-test',exchanges:exchanges.slice(0,2),exchange_aux:{2:[
      {seq:2,line:0,toolcall:{callId:'edit',kind:'edit',status:'complete',title:'Large edit',content:[{type:'diff',path:'example.ts',oldText:'old line\n'.repeat(400),newText:'NEW DIFF LINE\n'.repeat(400)}]}},
      {seq:2,line:0,toolcall:{callId:'shell',kind:'execute',status:'complete',title:'Shell',meta:{source:'userShell',command:'echo ready'},content:[{type:'text',text:'SHELL READY'}]}},
    ]}}),
  });
  return <I18nProvider><div style={{height:'100vh',display:'flex',flexDirection:'column'}}>{shown && <SessionViewer session={session} rootId="test" targetSeq={target}/>}</div></I18nProvider>;
}
createRoot(document.getElementById('root')!).render(<Check/>);

Object.assign(window, { runStreamingTextChecks: async () => {
  const settle = () => new Promise(resolve => setTimeout(resolve, 80));
  // 虚拟化路径下新内容要等一次测量/重排才落到 DOM（实测移动视口上偶尔 >80ms），
  // 固定 sleep 会让这条检查随机红 —— 改成「等到出现、最多 2s」，超时才算失败。
  const waitFor = async (predicate: () => boolean) => {
    for (let waited = 0; waited < 2000; waited += 50) {
      if (predicate()) return true;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    return predicate();
  };
  for (const long of [false, true]) {
    const prefix = long ? exchanges : exchanges.slice(0, 1);
    let content = 'Stable paragraph\n\n```js\nconst answer = 42;\n```\n\nStreaming';
    const update = (step: number) => (window as any).check.replaceSession({
      key: `streaming-${long}`, exchange_aux: long ? exchange_aux : {},
      // `seq: 0` 才是**真实流式行的形状**（瞬时行）。用正 seq 建模会走持久行路径：
      // 窗口态只装得下已落盘的行（`isPersistedSeq` 过滤 + `computeTailOverlay` 按 seq
      // 去重），同一 seq 的新对象被丢弃，内容永远停在首帧 —— 那是 fixture 的错，
      // 不是产品的错（2026-10-27 核实）。
      exchanges: [...prefix, {seq: 0, role: 'agent', content, timestamp: `2026-09-29T00:00:${String(step).padStart(2, '0')}Z`}],
    });
    update(0);
    await settle();
    // 瞬时行没有 `data-session-seq`（seq=0 不落属性），按正文定位。
    const locate = () => {
      const markdown = [...document.querySelectorAll('.markdown-viewer')]
        .find(node => node.textContent?.includes('Stable paragraph'));
      return { markdown, paragraph: markdown?.querySelector('p'), code: markdown?.querySelector('pre') };
    };
    const { markdown, paragraph, code } = locate();
    if (!markdown || !paragraph || !code) throw new Error('Missing streaming fixture nodes');
    for (let step = 1; step <= 16; step++) {
      content += ` chunk ${step}`;
      update(step);
      await settle();
      const now = locate();
      if (now.markdown !== markdown || now.paragraph !== paragraph || now.code !== code) {
        throw new Error(`Streaming Markdown nodes remounted (virtual=${long})`);
      }
      if (!(await waitFor(() => !!markdown.textContent?.includes(`chunk ${step}`)))) {
        throw new Error('Streaming content did not update');
      }
    }
  }
  return {passed: true, modes: ['short', 'virtual'], updatesPerMode: 16};
}});

Object.assign(window, { runScrollIntentChecks: async () => {
  const settle = (ms = 250) => new Promise(resolve => setTimeout(resolve, ms));
  const assert = (condition: boolean, message: string) => { if (!condition) throw new Error(message); };
  const el = [...document.querySelectorAll('div')].find(node => getComputedStyle(node).overflowY === 'auto')!;
  const gap = () => el.scrollHeight - el.clientHeight - el.scrollTop;
  await settle();
  for (const input of ['wheel', 'touch']) {
    el.scrollTop = el.scrollHeight;
    await settle();
    if (input === 'touch') {
      el.dispatchEvent(new TouchEvent('touchstart', {bubbles: true, touches: [new Touch({identifier: 1, target: el, clientY: 200})]}));
    }
    for (let step = 1; step <= 12; step++) {
      if (input === 'wheel') {
        el.dispatchEvent(new WheelEvent('wheel', {bubbles: true, deltaY: -5}));
      } else {
        el.dispatchEvent(new TouchEvent('touchmove', {bubbles: true, touches: [new Touch({identifier: 1, target: el, clientY: 200 + step * 5})]}));
      }
      el.scrollTop -= 5;
      await settle(40);
      assert(gap() >= 4, `${input}: small upward scroll snapped back to bottom`);
    }
    if (input === 'touch') el.dispatchEvent(new TouchEvent('touchend', {bubbles: true, touches: []}));
    assert(gap() >= 45, `${input}: slow upward scrolling made no progress`);
    const top = el.scrollTop;
    (window as any).check.append();
    window.dispatchEvent(new Event('mindfs:safe-area-updated'));
    await settle();
    assert(Math.abs(el.scrollTop - top) < 2, `${input}: append/viewport change stole reading position`);
    el.scrollTop = el.scrollHeight;
    await settle();
    (window as any).check.append();
    await settle();
    assert(gap() < 2, `${input}: returning to bottom did not resume following`);
  }
  return {passed: true, inputs: ['wheel', 'touch'], upwardSteps: 12};
}});

// 2026-10-27 回归：三个报障都只在「窗口分页 + 虚拟化」这条路径上出现。
//   ① 上翻触发 loadMore 后视口整体跳一大截；
//   ② 「回到底部」按钮停在半路（不是真实最底部）；
//   ③ 按钮显隐不与页面实际位置挂钩（滚轮到底不消失、拖滚动条上滑不出现）；
//   ④ **一次手势只翻一页**：滚到顶部后松手不动，不得连着加载（2026-10-27 真机：
//      真实会话一页能展开成 70+ 条目，锚行前插后被推出 overscan 永不挂载，
//      两条补偿都不走，scrollTop 停在 0，哨兵持续 intersecting，一次上滑翻完 29 页）。
Object.assign(window, { runPagingChecks: async (opts: any = {}) => {
  const settle = (ms = 350) => new Promise(resolve => setTimeout(resolve, ms));
  const problems: string[] = [];
  const check = (condition: boolean, message: string) => { if (!condition) problems.push(message); };
  const scroller = () => [...document.querySelectorAll('div')].find(node => getComputedStyle(node).overflowY === 'auto')!;
  const gap = () => { const el = scroller(); return el.scrollHeight - el.clientHeight - el.scrollTop; };
  const prepends = () => (window as any).check.prepends();
  const jumpButton = () => [...document.querySelectorAll('button')]
    .find(node => /jump|latest|底部|最新/i.test(node.getAttribute('aria-label') || ''));

  (window as any).check.paging();
  // 首屏窗口要等一次 stub 往返 + 虚拟器测量；固定 sleep 在慢机器上会读到「还没渲染」，
  // 那样后面的断言全在空 DOM 上跑（而且会误报成产品缺陷）。改成「等到出现，最多 3s」。
  for (let waited = 0; waited < 3000; waited += 50) {
    if (document.querySelectorAll('[data-index]').length > 0) break;
    await settle(50);
  }
  await settle(300);
  check(document.querySelectorAll('[data-timeline-key]').length > 0, 'paging: first window did not render');
  check(gap() < 2, 'paging: initial view should sit at the bottom');
  check(!jumpButton(), 'paging: jump button must be hidden at the bottom');
  // 夹具必须真的把虚拟化顶开：条目数不到阈值就退回全量渲染，锚行永不卸载，
  // 「一次上滑连翻到底」这条路径根本走不到（这正是它逃过回归的原因）。
  check(
    document.querySelectorAll('[data-index]').length > 0 &&
      document.querySelectorAll('[data-index]').length < 80,
    'paging: fixture must run on the virtualized path (mounted rows 0..80)',
  );

  // ① 连续上翻 N 页：每轮先滚到顶（一次手势），回包后再比对锚行的真实矩形。
  const drifts: number[] = [];
  let pages = 0;
  const wanted = Number(opts.pages || 6);
  for (let attempt = 0; attempt < 60 && pages < wanted; attempt++) {
    const before = prepends();
    scroller().scrollTop = 0;
    for (let waited = 0; waited < 2000 && prepends() === before; waited += 50) await settle(50);
    if (prepends() === before) continue;
    await settle(400);
    pages += 1;
    if (lastPrepend) {
      const row = document.querySelector(`[data-timeline-key="${CSS.escape(lastPrepend.key)}"]`);
      if (row) drifts.push(Math.round(Math.abs(row.getBoundingClientRect().top - lastPrepend.top)));
    }
  }
  check(pages >= wanted, `paging: only ${pages} upward pages loaded, expected ${wanted}`);
  const maxDrift = drifts.length ? Math.max(...drifts) : 0;
  check(maxDrift <= 40, `paging: prepend jumped the viewport by ${maxDrift}px (max 40 = one line)`);
  // 每翻一页都必须真的把 anchor 拉回原位：滚到顶后视口不该还停在 0
  check(scroller().scrollTop > 0, 'paging: viewport stuck at the very top after prepending');
  // 页数必须与请求数一一对应：一次上滑只该产生一次前插请求。失控时一轮手势会把
  // 剩余历史全部拉完（实测 29 次），而 pages 只按「轮次」计数，单看 pages 看不出来。
  check(
    prepends() === pages,
    `paging: ${prepends()} prepend requests for ${pages} gestures — one gesture loaded several pages`,
  );
  if (opts.only === 'prepend') {
    return { passed: problems.length === 0, pages, drifts, maxDrift, problems };
  }

  // ④ 一次手势只翻一页：滚到顶后**完全不再操作**，等够两页的往返时间。
  const beforeIdle = prepends();
  scroller().scrollTop = 0;
  await settle(2000);
  const idlePages = prepends() - beforeIdle;
  check(idlePages === 1, `paging: one upward gesture loaded ${idlePages} pages (expected exactly 1)`);

  // ② 点击「回到底部」必须精准落到真实最底部
  await settle(300);
  scroller().scrollTop = Math.max(0, scroller().scrollTop - 800);
  await settle(500);
  check(!!jumpButton(), 'paging: jump button should appear after scrolling up');
  jumpButton()?.click();
  await settle(900);
  const clickGap = gap();
  check(clickGap === 0, `paging: jump-to-bottom stopped ${clickGap}px short of the real bottom`);
  check(!jumpButton(), 'paging: jump button should disappear once at the bottom');

  // ③ 显隐只跟实际离底距离有关，任何输入方式都算（这里用「拖滚动条」= 直接写 scrollTop）
  const el = scroller();
  el.scrollTop = Math.max(0, el.scrollTop - 50);
  await settle(500);
  const upGap = gap();
  check(upGap >= 40, `paging: fixture failed to scroll up (gap ${upGap})`);
  check(!!jumpButton(), 'paging: dragging the scrollbar up must reveal the jump button');
  const hiddenWhileUp = !jumpButton();
  el.scrollTop = el.scrollHeight;
  await settle(500);
  check(!jumpButton(), 'paging: dragging back to the bottom must hide it without a click');

  return { passed: problems.length === 0, pages, drifts, maxDrift, idlePages, clickGap, upGap, hiddenWhileUp, problems };
}});

// Run via a browser console or agent-browser eval. No server/agent data is used.
Object.assign(window, { runVirtualizationChecks: async () => {
  const api = () => (window as any).check;
  const settle = () => new Promise(resolve => setTimeout(resolve, 400));
  const assert = (condition: boolean, message: string) => { if (!condition) throw new Error(message); };
  const scroller = () => [...document.querySelectorAll('div')].find(node => getComputedStyle(node).overflowY === 'auto')!;
  const rows = () => document.querySelectorAll('[data-index]').length;
  await settle();
  assert(rows() > 0 && rows() < 80, 'Only viewport rows should mount');
  const initialRows = rows();
  assert(scroller().scrollHeight - scroller().scrollTop - scroller().clientHeight < 2, 'Initial view should be at bottom');
  assert(requests === 0, 'Collapsed cards should not load remote details');
  api().jump(21);
  await settle();
  const target = document.querySelector('[data-session-seq="21"]')!;
  assert(!!target && target.getBoundingClientRect().top > 0 && target.getBoundingClientRect().bottom < innerHeight, 'Search target should be visible');
  // 分组（G-AT）：连续同类工具卡折成一张组卡，Tool 10-0 在 seq 22 上面的「100 reads」里。
  // 组卡收起时不渲染子卡，所以要先展开它 —— 那是产品行为，不是 fixture 的缺陷。
  const expandGroupBefore = async (seq: number) => {
    const mounted = [...document.querySelectorAll('[data-index]')];
    const at = mounted.findIndex(row => row.querySelector(`[data-session-seq="${seq}"]`));
    mounted[at - 1]?.querySelector('button')?.click();
    await settle();
  };
  await expandGroupBefore(22);
  const tool = [...document.querySelectorAll('button')].find(node => node.textContent?.trim() === 'Tool 10-0')!;
  tool.click();
  await settle();
  assert(requests === 1 && document.body.innerText.includes('REMOTE DETAIL call-10-0'), 'Expanding should load details');
  api().jump(41);
  await settle();
  assert(!document.body.innerText.includes('REMOTE DETAIL call-10-0'), 'Offscreen details should unmount');
  api().jump(21);
  await settle();
  // 组卡展开态与已拉详情都按会话缓存在 toolViewCache 里（CUSTOM(G-AT)），
  // 重挂回来必须原样恢复且不重新请求 —— `976a5d0f` 合上游时这段丢了，回归就靠这条。
  assert(requests === 1 && document.body.innerText.includes('REMOTE DETAIL call-10-0'), 'Expansion and cached details should survive remount');
  const readingTop = scroller().scrollTop;
  api().append();
  await settle();
  assert(Math.abs(scroller().scrollTop - readingTop) < 2, 'Appending should preserve reading position');
  // Re-entering constructs a new viewer without mounting the full history.
  api().toggle();
  await settle();
  api().jump(0);
  api().toggle();
  await settle();
  assert(rows() > 0 && rows() < 80, 'Re-entry should remain virtualized');
  api().append();
  await settle();
  assert(scroller().scrollHeight - scroller().scrollTop - scroller().clientHeight < 2, 'Appending at bottom should follow the tail');
  const finalRows = rows();
  api().question();
  await settle();
  const draft = document.querySelector('textarea');
  assert(!!draft, 'Pending question should display its input');
  api().jump(21);
  await settle();
  assert(draft === document.querySelector('textarea') && draft!.isConnected, 'Pending question input should survive scrolling out of view');
  api().jump(0);
  api().short();
  await settle();
  assert(rows() === 0, 'Short sessions should use the regular layout');
  assert(!document.body.innerText.includes('NEW DIFF LINE'), 'Folded diff should not render');
  assert(document.body.innerText.includes('SHELL READY'), 'User shell should still expand by default');
  [...document.querySelectorAll('button')].find(node => node.textContent?.includes('Large edit'))!.click();
  await settle();
  assert(document.body.innerText.includes('NEW DIFF LINE'), 'Expanded diff should render');
  assert(requests === 1, 'Local diff and shell output should not request remote details');
  return {passed: true, toolCalls: 3000, initialRows, finalRows, detailRequests: requests};
}});
