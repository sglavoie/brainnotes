import { CommandSwitcher } from "./command-switcher-dialog";
import { ShortcutSheet } from "./shortcut-sheet";
import type { KeyboardToolRequest } from "./keyboard-tools-loader";

let switcher: CommandSwitcher | undefined;
let sheet: ShortcutSheet | undefined;
document.addEventListener("bn:keyboard-tools", (event) => {
  if (document.querySelector("dialog[open]")) return;
  const { action, origin } = (event as CustomEvent<KeyboardToolRequest>).detail;
  if (action === "sheet") { sheet ||= new ShortcutSheet(); sheet.open(origin); }
  else { switcher ||= new CommandSwitcher(); switcher.open(origin); }
});
