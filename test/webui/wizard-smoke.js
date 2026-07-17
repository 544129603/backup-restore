const fs = require("node:fs");
const vm = require("node:vm");

const app = fs.readFileSync("internal/webui/static/app.js", "utf8");
const wizard = fs.readFileSync("internal/webui/static/wizard.js", "utf8");
const assertions = String.raw`
function assert(condition, message) {
  if (!condition) throw new Error(message);
}

wizardState.resource = "policies";
wizardState.operation = "create";
wizardState.object = null;
wizardState.draft = resourceDefinitions.policies.template({repository: "repo"});
assert(path(wizardState.draft, "spec.selection.mode") === "Namespace", "policy must embed a Namespace selection by default");
assert(!path(wizardState.draft, "spec.scopeRef"), "policy must not contain scopeRef");
const policyHTML = renderObjectForm();
assert(policyHTML.includes("保护范围") && policyHTML.includes("PVC 快照"), "policy form must render selection and snapshot sections");
assert(validateWizardDraft(wizardState.draft).length === 0, "default merged policy must pass wizard validation");

wizardState.resource = "backup-tasks";
wizardState.draft = resourceDefinitions["backup-tasks"].template({policy: "daily-backup"});
assert(path(wizardState.draft, "spec.policyRef.name") === "daily-backup", "manual task must reference a policy");
assert(!path(wizardState.draft, "spec.repositoryRef") && !path(wizardState.draft, "spec.scopeRef"), "manual task must inherit repository and selection from policy");

wizardState.resource = "restore-tasks";
wizardState.draft = restoreTemplate("backup-record", []);
const restoreHTML = renderObjectForm();
assert(restoreHTML.includes("恢复来源") && restoreHTML.includes("恢复点"), "restore form must use recovery point terminology");

console.log("wizard-smoke: PASS");
`;

const documentStub = {
  addEventListener() {},
  getElementById() { return {addEventListener() {}}; },
};
const context = vm.createContext({
  console,
  document: documentStub,
  window: {setInterval() {}},
  navigator: {clipboard: {writeText: async () => {}}},
  URLSearchParams,
  Date,
  Math,
  JSON,
  Set,
  Map,
  structuredClone,
  setTimeout,
  clearTimeout,
});

vm.runInContext(`${app}\n${wizard}\n${assertions}`, context, {filename: "wizard-smoke.bundle.js"});
