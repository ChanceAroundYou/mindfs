/**
 * 选区钳制：让一段**在聊天正文里开始**的拖选，不会把正文外的界面（会话标题、header、
 * 侧栏、输入条）也带上。
 *
 * 为什么需要：浏览器拖选一旦把指针拖出滚动容器，会继续把选区向外扩——向上拖到
 * 标题栏时，标题和上方全部正文一起被选上，用户只是想复制一段正文，复制出来的却是
 * 一整篇。视觉上可以靠 `user-select: none` 让 chrome 不可选，但它挡不住「选区跨到
 * 容器外再被浏览器归一化」的路径，且任何一个漏配 none 的区域都会重新暴露；所以这里
 * 再做一层：选区一端落在容器内、另一端落到容器外时，把**外侧那一端**拉回容器边界。
 *
 * 不做的事：两端都在容器外（选区属于别处，例如侧栏里选会话名）一律不碰；两端都在
 * 容器内是常规选区，也不碰。这样即便同时挂着多个会话视图，各自只处理真正压到自己
 * 身上的选区，不会互相抢。
 */

/** 选区端点（节点 + 偏移）。 */
export type SelectionPoint = { node: Node; offset: number };

const TEXT_NODE = 3;

/** 节点是不是 root 本身或其后代。 */
export function isInsideRoot(root: Node, node: Node | null): boolean {
  if (!node) return false;
  return node === root || root.contains(node);
}

/** 容器内第一个非空文本节点（按文档顺序深度优先）。 */
export function firstTextNode(root: Node): Text | null {
  if (root.nodeType === TEXT_NODE) {
    const text = root as Text;
    return text.data.length > 0 ? text : null;
  }
  const children = root.childNodes;
  for (let i = 0; i < children.length; i += 1) {
    const found = firstTextNode(children[i]);
    if (found) return found;
  }
  return null;
}

/** 容器内最后一个非空文本节点（按文档顺序深度优先，倒着找）。 */
export function lastTextNode(root: Node): Text | null {
  if (root.nodeType === TEXT_NODE) {
    const text = root as Text;
    return text.data.length > 0 ? text : null;
  }
  const children = root.childNodes;
  for (let i = children.length - 1; i >= 0; i -= 1) {
    const found = lastTextNode(children[i]);
    if (found) return found;
  }
  return null;
}

/** 容器边界上的两个端点；没有文本时退化成容器元素本身的起止偏移。 */
export function textBoundaries(root: Node): { start: SelectionPoint; end: SelectionPoint } {
  const first = firstTextNode(root);
  const last = lastTextNode(root);
  return {
    start: first ? { node: first, offset: 0 } : { node: root, offset: 0 },
    end: last ? { node: last, offset: last.data.length } : { node: root, offset: root.childNodes.length },
  };
}

/**
 * 把「一端在 root 内、另一端跑到 root 外」的选区拉回 root 边界。
 *
 * 返回是否真的改过选区。区间在文档顺序上 start ≤ end，因此外侧那一端要么在 root
 * 之前（start 侧）、要么在 root 之后（end 侧），按位置贴到最近的边界即可。
 */
export function clampSelectionToRoot(root: Element | null, selection: Selection | null): boolean {
  if (!root || !selection || selection.rangeCount === 0) return false;
  const range = selection.getRangeAt(0);
  if (range.collapsed) return false;
  // 最快路径：commonAncestor 在容器内 ⇒ 两端都在容器内，不需要动。
  if (isInsideRoot(root, range.commonAncestorContainer)) return false;
  const startInside = isInsideRoot(root, range.startContainer);
  const endInside = isInsideRoot(root, range.endContainer);
  // 两端同侧：要么都在容器内（上面已返回），要么这段选区根本不归本容器管。
  if (startInside === endInside) return false;
  const doc = root.ownerDocument;
  if (!doc) return false;
  const bounds = textBoundaries(root);
  const next = doc.createRange();
  if (startInside) {
    next.setStart(range.startContainer, range.startOffset);
    next.setEnd(bounds.end.node, bounds.end.offset);
  } else {
    next.setStart(bounds.start.node, bounds.start.offset);
    next.setEnd(range.endContainer, range.endOffset);
  }
  selection.removeAllRanges();
  selection.addRange(next);
  return true;
}