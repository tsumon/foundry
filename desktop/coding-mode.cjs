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
          "This enables configuration only. It makes no model calls and runs no Docker commands. Import, model use, Docker execution, and delivery require separate approvals inside the workbench.",
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
