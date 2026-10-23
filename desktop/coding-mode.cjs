"use strict";

function createCodingMenuItem(dialog, launch) {
  return {
    label: "Controlled Coding Workbench",
    click: async () => {
      const choice = await dialog.showMessageBox({
        type: "question",
        title: "Open Controlled Coding Workbench?",
        message: "Open live coding configuration?",
        detail:
          "This opens configuration only. It starts no VM, runs no Docker commands, and makes no model calls. Sandbox readiness, testing, import, model use, and delivery require their own approvals inside the workbench.",
        buttons: ["Open Coding Workbench", "Cancel"],
        defaultId: 1,
        cancelId: 1,
        noLink: true,
      });
      if (choice.response === 0) await launch("coding");
    },
  };
}

module.exports = { createCodingMenuItem };
