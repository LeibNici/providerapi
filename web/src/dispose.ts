/** Run cleanup when `el` is removed from the document (navigation or re-render). */
export function whenDisconnected(el: Element, cleanup: () => void): void {
  if (!el.isConnected) {
    cleanup();
    return;
  }
  const observer = new MutationObserver(() => {
    if (!el.isConnected) {
      observer.disconnect();
      cleanup();
    }
  });
  let node: Node | null = el.parentNode;
  while (node) {
    observer.observe(node, { childList: true });
    node = node.parentNode;
  }
}
