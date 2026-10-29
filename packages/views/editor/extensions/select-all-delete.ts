import { Extension } from "@tiptap/core";
import { AllSelection } from "@tiptap/pm/state";

// shortcut: Tiptap <3.29 leaves AllSelection after deletion, remove once all editors use 3.29+.
export const SelectAllDelete = Extension.create({
  name: "selectAllDelete",
  priority: 101,
  addKeyboardShortcuts() {
    const deleteSelection = () => {
      if (!(this.editor.state.selection instanceof AllSelection)) return false;
      return this.editor.chain().deleteSelection().setTextSelection(1).run();
    };
    return { Backspace: deleteSelection, Delete: deleteSelection };
  },
});
