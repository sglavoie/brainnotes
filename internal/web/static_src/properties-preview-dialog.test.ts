// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { createPreviewDialog } from "./properties-preview-dialog";
import { mutationFixture, requireElement } from "./properties-workbench.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
});

const openDialog = (returnFocus?: Element | null) => {
  const host = document.createElement("div");
  document.body.appendChild(host);
  const dialog = createPreviewDialog(document, host);
  const onCancel = vi.fn();
  const onConfirm = vi.fn();
  dialog.open({
    title: "Remove status",
    summary: "",
    confirmLabel: "Remove property",
    result: mutationFixture(),
    returnFocus,
    onCancel,
    onConfirm,
  });
  return { dialog, onCancel, onConfirm };
};

describe("property preview dialog", () => {
  it("is a native modal dialog that starts on Cancel", () => {
    const { dialog } = openDialog();
    expect(dialog.element).toBeInstanceOf(HTMLDialogElement);
    expect((dialog.element as HTMLDialogElement).open).toBe(true);
    expect(requireElement("[data-property-preview-confirm]", HTMLButtonElement).disabled).toBe(false);
    expect(document.activeElement).toBe(requireElement("[data-property-preview-cancel]", HTMLButtonElement));
  });

  it("returns focus to the named opener even if it was disabled while previewing", () => {
    const opener = document.createElement("button");
    document.body.appendChild(opener);
    const { dialog, onCancel } = openDialog(opener);
    requireElement("[data-property-preview-cancel]", HTMLButtonElement).click();
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(dialog.isOpen()).toBe(false);
    expect((dialog.element as HTMLDialogElement).open).toBe(false);
    expect(document.activeElement).toBe(opener);
  });

  it("treats the browser's own cancel request as Cancel", () => {
    const { dialog, onCancel, onConfirm } = openDialog();
    const request = new Event("cancel", { cancelable: true });
    dialog.element.dispatchEvent(request);
    expect(request.defaultPrevented).toBe(true);
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
    expect(dialog.isOpen()).toBe(false);
  });
});
