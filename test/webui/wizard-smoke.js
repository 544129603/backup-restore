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

const detailMetadata = {name: "daily-backup", uid: "uid-1", resourceVersion: "12", generation: 2, labels: {team: "platform"}};
const detailPolicy = {
  metadata: detailMetadata,
  spec: {
    enabled: true, suspend: false, repositoryRef: {name: "local-repository"}, schedule: {cron: "0 2 * * *", timezone: "Asia/Shanghai"},
    selection: {mode: "Namespace", includeNamespaces: ["default"], resources: {include: ["deployments.apps"]}, pvc: {enabled: true}},
    retention: {maxCopies: 7, minCopies: 1, maxAgeDays: 30}, retryPolicy: {maxAttempts: 3},
  },
  status: {phase: "Ready", selectionPreview: {namespaceCount: 1, resourceObjectCount: 8}, conditions: [{type: "Ready", status: "True", reason: "Reconciled"}]},
};
const policyDetailHTML = renderObjectDetail("policies", detailPolicy);
assert(policyDetailHTML.includes("调度与执行") && policyDetailHTML.includes("保护范围") && policyDetailHTML.includes("范围预估"), "policy detail must expose schedule, embedded selection and preview");
assert(policyDetailHTML.includes("local-repository") && policyDetailHTML.includes("状态条件"), "policy detail must expose repository relation and conditions");

const taskDetailHTML = renderObjectDetail("backup-tasks", {
  metadata: {...detailMetadata, name: "run-1"},
  spec: {policyRef: {name: "daily-backup"}, repositoryRef: {name: "local-repository"}, trigger: "Manual", selectionSnapshot: detailPolicy.spec.selection},
  status: {phase: "Completed", step: "GeneratingRecord", progress: {percent: 100, totalResources: 8, processedResources: 8}, recordRef: {name: "point-1"}, checkpoints: [{step: "Upload", key: "archive", completed: true}]},
});
assert(taskDetailHTML.includes("执行进度") && taskDetailHTML.includes("执行范围快照") && taskDetailHTML.includes("point-1"), "backup task detail must expose progress, frozen selection and record output");

const recordDetailHTML = renderObjectDetail("records", {
  metadata: {...detailMetadata, name: "point-1"},
  spec: {policyRef: {name: "daily-backup"}, sourceTaskRef: {name: "run-1"}, repositoryRef: {name: "local-repository"}, source: {clusterRef: "docker-desktop", namespaces: ["default"]}, inventory: {resourceCount: 8, pvcCount: 1, backupBytes: 1024}, snapshots: []},
  status: {phase: "Available", restorable: true, conditions: []},
});
assert(recordDetailHTML.includes("备份内容清单") && recordDetailHTML.includes("完整性与安全") && recordDetailHTML.includes("run-1"), "record detail must expose inventory, integrity and source task");

const restoreDetailHTML = renderObjectDetail("restore-tasks", {
  metadata: {...detailMetadata, name: "restore-1"},
  spec: {backupRecordRef: {name: "point-1"}, targetClusterRef: "docker-desktop", conflictPolicy: {default: "Skip"}, namespaceMapping: {default: "default-restored"}},
  status: {phase: "Completed", progress: {total: 8, processed: 8}, plan: {totalObjects: 8, conflictCount: 0}},
});
assert(restoreDetailHTML.includes("恢复进度") && restoreDetailHTML.includes("冲突与资源规则") && restoreDetailHTML.includes("default → default-restored"), "restore detail must expose execution plan and namespace mapping");

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
