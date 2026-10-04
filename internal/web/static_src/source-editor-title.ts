// Keep late save responses from changing the title after the editor is gone.
export const createEditorTitle = (doc: Document) => {
  const original = doc.title;
  let disposed = false;
  return {
    update(pending: boolean): void {
      if (!disposed) doc.title = pending ? `• ${original}` : original;
    },
    dispose(): void {
      if (disposed) return;
      disposed = true;
      doc.title = original;
    },
  };
};
