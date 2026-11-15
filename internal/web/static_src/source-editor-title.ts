// Keep late save responses from changing the title after the editor is gone.
export const createEditorTitle = (doc: Document) => {
  const original = doc.title;
  let disposed = false;
  return {
    update(pending: boolean): void {
      const next = pending ? `• ${original}` : original;
      if (!disposed && doc.title !== next) doc.title = next;
    },
    dispose(): void {
      if (disposed) return;
      disposed = true;
      doc.title = original;
    },
  };
};
