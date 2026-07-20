const API_BASE = "/api/resources";

const state = {
  currentView: "overview",
  items: [],
  filteredItems: [],
  clusterRef: "docker-desktop",
  editor: { mode: "create", resource: null, name: null },
  detail: null,
  deletion: null,
  policyRelations: {},
};

const terminalPhases = new Set(["Completed", "PartiallyFailed", "Failed", "Cancelled", "Available", "PartiallyAvailable", "Broken", "Expired", "Deleted"]);

const resourceDefinitions = {
  repositories: {
    title: "备份仓库", singular: "备份仓库", kind: "BackupRepository", creatable: true, editable: true,
    columns: [
      ["类型", o => path(o, "spec.type", "—")],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["可用容量", o => path(o, "status.capacityKnown", false) ? formatBytes(path(o, "status.availableBytes", 0)) : "未知"],
      ["策略引用", o => String(path(o, "status.activePolicyCount", 0))],
      ["最近检查", o => formatDate(path(o, "status.lastCheckTime", null))],
    ],
    actions: o => [action("refresh", "连通检查"), action("edit", "编辑"), action("delete", "删除", "danger")],
    template: () => baseObject("BackupRepository", "local-repository", {
      clusterRef: state.clusterRef, type: "Local", enabled: true,
      local: {mode: "HostPath", path: "/repository", nodeName: "desktop-control-plane", uid: 65532, gid: 65532},
      compression: {algorithm: "Gzip", level: 6}, encryption: {enabled: false},
      healthCheckInterval: "30m", timeout: "30s", retryCount: 3, minimumFreeBytes: "1Gi", deletionProtection: true,
    }),
  },
  policies: {
    title: "备份策略", singular: "备份策略", kind: "BackupPolicy", creatable: true, editable: true,
    columns: [
      ["保护范围", o => selectionCell(path(o, "spec.selection", {}))],
      ["仓库", o => escapeHTML(path(o, "spec.repositoryRef.name", "—"))],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["最近执行 / 恢复点", o => latestPolicyRunCell(o)],
      ["调度", o => policyScheduleCell(o)],
    ],
    actions: o => [action("run", "立即执行"), action(path(o, "spec.suspend", false) ? "resume" : "suspend", path(o, "spec.suspend", false) ? "启用" : "停用"), action("edit", "编辑"), action("delete", "删除", "danger")],
    template: refs => baseObject("BackupPolicy", "daily-backup", {
      clusterRef: state.clusterRef,
      repositoryRef: {name: refs.repository || "local-repository"},
      selection: {
        mode: "Namespace", includeNamespaces: ["default"], excludeNamespaces: [],
        resources: {include: ["configmaps", "secrets", "services", "deployments.apps"], exclude: ["events", "pods"]},
        includeClusterResources: false, includeSecrets: true, includeCRDs: false, includeCustomResources: true,
        pvc: {enabled: false, snapshotTimeout: "10m", failurePolicy: "ContinueAndMarkPartial", lifecycle: "RetainAfterRecordDeletion"},
        consistencyMode: "CrashConsistent",
      },
      schedule: {cron: "0 2 * * *", timezone: "Asia/Shanghai"}, enabled: true, suspend: false,
      concurrencyPolicy: "Forbid", missedRunPolicy: "RunOnce", startingDeadline: "1h", maxCatchUpRuns: 1,
      retention: {maxCopies: 7, minCopies: 1, maxAgeDays: 30, deleteSnapshots: false},
      retryPolicy: {maxAttempts: 3, backoff: "30s", maxBackoff: "10m"}, timeout: "4h",
    }),
  },
  "backup-tasks": {
    title: "执行历史", singular: "备份任务", kind: "BackupTask", creatable: true, editable: false,
    columns: [
      ["触发方式", o => path(o, "spec.trigger", "Manual")],
      ["备份来源", o => path(o, "spec.source.type", "OneTime") === "Policy" ? escapeHTML(path(o, "spec.source.policyRef.name", "—")) : '<span class="yes-text">一次性备份</span>'],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["恢复点", o => taskRecordCell(o)],
      ["进度", o => progressCell(path(o, "status.progress.percent", 0))],
      ["备份大小", o => formatBytes(path(o, "status.backupBytes", 0))],
      ["开始时间", o => formatDate(path(o, "status.startedAt", path(o, "metadata.creationTimestamp", null)))],
    ],
    actions: o => [action("view", "详情"), ...(!terminalPhases.has(path(o, "status.phase", "")) ? [action("cancel", "取消", "danger")] : []), action("delete", "删除", "danger")],
    template: refs => baseObject("BackupTask", `onetime-${compactTimestamp()}`, {
      clusterRef: state.clusterRef, trigger: "Manual", source: {type: "OneTime"},
      backupSpec: {
        repositoryRef: {name: refs.repository || "local-repository"},
        selection: {
          mode: "Namespace", includeNamespaces: ["default"], excludeNamespaces: [],
          resources: {include: ["configmaps", "services", "deployments.apps"], exclude: ["events", "pods"]},
          includeClusterResources: false, includeSecrets: false, includeCRDs: false, includeCustomResources: true,
          pvc: {enabled: false, snapshotTimeout: "10m", failurePolicy: "ContinueAndMarkPartial", lifecycle: "RetainAfterRecordDeletion"},
          consistencyMode: "CrashConsistent",
        },
        retention: {maxCopies: 1, minCopies: 1, maxAgeDays: 30, deleteSnapshots: false},
        timeout: "4h", retryPolicy: {maxAttempts: 3, backoff: "30s", maxBackoff: "10m"},
        failurePolicy: "Continue", allowPartialRecord: true,
      },
      idempotencyKey: `webui/onetime/${Date.now()}`,
    }),
  },
  records: {
    title: "恢复点", singular: "恢复点", kind: "BackupRecord", creatable: false, editable: false,
    columns: [
      ["来源", o => `${path(o, "spec.sourceType", "OneTime") === "Policy" ? escapeHTML(path(o, "spec.policyRef.name", "—")) : "一次性备份"}<span class="object-sub">任务 ${escapeHTML(path(o, "spec.sourceTaskRef.name", "—"))}</span>`],
      ["可用性", o => statusChip(path(o, "status.phase", "Pending"))],
      ["可恢复", o => path(o, "status.restorable", false) ? `<span class="yes-text">是</span>` : "否"],
      ["资源 / PVC", o => `${path(o, "spec.inventory.resourceCount", 0)} / ${path(o, "spec.inventory.pvcCount", 0)}`],
      ["大小", o => formatBytes(path(o, "spec.inventory.backupBytes", 0))],
      ["创建时间", o => formatDate(path(o, "metadata.creationTimestamp", null))],
    ],
    actions: o => [action("view", "详情"), ...(path(o, "status.restorable", false) ? [action("restore", "恢复")] : []), action("verify", "校验"), action("delete", "删除", "danger")],
  },
  "restore-tasks": {
    title: "恢复任务", singular: "恢复任务", kind: "RestoreTask", creatable: true, editable: false,
    columns: [
      ["恢复点", o => path(o, "spec.backupRecordRef.name", "—")],
      ["模式", o => `${escapeHTML(path(o, "spec.mode", "Original"))}${path(o, "spec.dryRun", false) ? '<span class="object-sub">DryRun</span>' : ""}`],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["进度", o => restoreProgress(o)],
      ["冲突策略", o => path(o, "spec.conflictPolicy.default", "Skip")],
      ["开始时间", o => formatDate(path(o, "status.startedAt", path(o, "metadata.creationTimestamp", null)))],
    ],
    actions: o => [action("view", "详情"), ...(!terminalPhases.has(path(o, "status.phase", "")) ? [action("cancel", "取消", "danger")] : []), action("delete", "删除", "danger")],
    template: refs => restoreTemplate(refs.record || "backup-record", []),
  },
  configs: {
    title: "全局配置", singular: "全局配置", kind: "BackupPluginConfig", creatable: true, editable: true,
    columns: [
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["备份并发", o => String(path(o, "spec.concurrency.maxBackupTasks", 3))],
      ["恢复并发", o => String(path(o, "spec.concurrency.maxRestoreTasks", 1))],
      ["API QPS", o => String(path(o, "spec.kubernetesClient.qps", 20))],
      ["Operator 版本", o => path(o, "status.operatorVersion", "—")],
    ],
    actions: () => [action("edit", "编辑")],
    template: () => baseObject("BackupPluginConfig", "cluster", {
      defaultTimezone: "Asia/Shanghai", defaultBackupTimeout: "4h", defaultRestoreTimeout: "4h", defaultSnapshotTimeout: "10m",
      concurrency: {maxBackupTasks: 3, maxRestoreTasks: 1, maxSnapshotsPerTask: 10, maxRepositoryOperations: 4},
      kubernetesClient: {qps: 20, burst: 40, pageSize: 500},
      security: {allowedSecretNamespaces: ["backup-system"], requireEncryptionForSecrets: true, allowInsecureSFTP: false, hookExecutionEnabled: false},
      garbageCollection: {interval: "1h", stagingGracePeriod: "24h", terminalTaskTTLDays: 90}, workspacePath: "/workspace", logLevel: "info",
    }),
  },
};

document.addEventListener("DOMContentLoaded", () => {
  bindEvents();
  checkHealth();
  navigate("overview");
  window.setInterval(() => {
    if (![...document.querySelectorAll("dialog")].some(d => d.open)) refreshCurrent(true);
  }, 15000);
});

function bindEvents() {
  document.getElementById("navigation").addEventListener("click", event => {
    const button = event.target.closest("[data-view]");
    if (button) navigate(button.dataset.view);
  });
  document.querySelectorAll("[data-refresh]").forEach(button => button.addEventListener("click", () => refreshCurrent()));
  document.getElementById("refresh-button").addEventListener("click", () => refreshCurrent());
  document.getElementById("create-button").addEventListener("click", () => openObjectWizard("create", state.currentView));
  document.getElementById("search-input").addEventListener("input", filterAndRender);
  document.getElementById("resource-body").addEventListener("click", handleTableAction);
  document.getElementById("editor-form").addEventListener("submit", saveEditor);
  document.querySelectorAll("[data-close-detail]").forEach(button => button.addEventListener("click", () => document.getElementById("detail-dialog").close()));
  document.getElementById("detail-edit").addEventListener("click", () => {
    const detail = state.detail;
    document.getElementById("detail-dialog").close();
    if (detail) openObjectWizard("edit", detail.resource, detail.object);
  });
  document.getElementById("detail-tabs").addEventListener("click", event => {
    const tab = event.target.closest("[data-detail-tab]");
    if (tab && !tab.classList.contains("hidden")) switchDetailTab(tab.dataset.detailTab);
  });
  document.getElementById("copy-json").addEventListener("click", async () => {
    await navigator.clipboard.writeText(document.getElementById("detail-json").textContent);
    toast("已复制", "对象 JSON 已复制到剪贴板");
  });
  document.getElementById("delete-form").addEventListener("submit", submitDelete);
  document.getElementById("detail-dialog").addEventListener("click", async event => {
    const button = event.target.closest("[data-related-resource]");
    if (!button) return;
    try {
      const object = await api(`${API_BASE}/${button.dataset.relatedResource}/${encodeURIComponent(button.dataset.relatedName)}`);
      showDetail(button.dataset.relatedResource, object);
    } catch (error) { toast("关联对象读取失败", error.message, true); }
  });
}

async function checkHealth() {
  try {
    const health = await api("/api/health");
    state.clusterRef = health.clusterRef || state.clusterRef;
    document.getElementById("cluster-ref").textContent = state.clusterRef || "全部集群";
    document.getElementById("health-text").textContent = "服务正常";
    document.getElementById("version-text").textContent = `Web UI ${health.version}`;
    document.getElementById("health-dot").className = "health-dot online";
  } catch (error) {
    document.getElementById("health-text").textContent = "连接异常";
    document.getElementById("version-text").textContent = error.message;
    document.getElementById("health-dot").className = "health-dot offline";
  }
}

function navigate(view) {
  state.currentView = view;
  document.querySelectorAll(".nav-item").forEach(button => button.classList.toggle("active", button.dataset.view === view));
  document.getElementById("overview-view").classList.toggle("hidden", view !== "overview");
  document.getElementById("resource-view").classList.toggle("hidden", view === "overview");
  document.getElementById("search-input").value = "";
  if (view === "overview") {
    document.getElementById("page-title").textContent = "运行概览";
    loadOverview();
    return;
  }
  const definition = resourceDefinitions[view];
  document.getElementById("page-title").textContent = definition.title;
  document.querySelector(".table-wrap table").dataset.resource = view;
  const createButton = document.getElementById("create-button");
  createButton.classList.toggle("hidden", !definition.creatable);
  createButton.textContent = `＋ 新建${definition.singular}`;
  loadResources(view);
}

async function refreshCurrent(silent = false) {
  if (!silent) toast("正在刷新", "已向 Kubernetes API 获取最新状态");
  if (state.currentView === "overview") return loadOverview();
  return loadResources(state.currentView, silent);
}

async function loadOverview() {
  const cards = document.getElementById("summary-cards");
  cards.innerHTML = Array.from({length: 4}, () => '<article class="summary-card"><p>正在加载</p><strong>—</strong><small>请稍候</small></article>').join("");
  try {
    const data = await api("/api/overview");
    const summaries = data.resources || {};
    const failedTasks = phaseCount(summaries["backup-tasks"], ["Failed", "PartiallyFailed"]) + phaseCount(summaries["restore-tasks"], ["Failed", "PartiallyFailed"]);
    cards.innerHTML = [
      card("备份仓库", summaries.repositories?.total || 0, `${phaseCount(summaries.repositories, ["Ready"])} 个可用`, "▣", "#2867f0", "#edf3ff"),
      card("可用恢复点", phaseCount(summaries.records, ["Available", "PartiallyAvailable"]), `共 ${summaries.records?.total || 0} 个恢复点`, "◇", "#169b62", "#eaf8f1"),
      card("运行中任务", runningCount(summaries), "备份与恢复", "▶", "#8b5cf6", "#f2edff"),
      card("异常任务", failedTasks, failedTasks ? "需要处理" : "运行健康", "!", failedTasks ? "#d64747" : "#21a58e", failedTasks ? "#fff0f0" : "#eaf9f6"),
    ].join("");
    renderRecent(data.recentTasks || []);
    renderHealth(summaries);
  } catch (error) {
    cards.innerHTML = '<article class="summary-card"><p>加载失败</p><strong>!</strong><small>请检查 Web UI 权限</small></article>';
    document.getElementById("recent-tasks").textContent = error.message;
    document.getElementById("health-summary").textContent = "无法读取资源状态";
    toast("概览加载失败", error.message, true);
  }
}

function card(label, value, hint, icon, color, tint) {
  return `<article class="summary-card" style="--card-color:${color};--card-tint:${tint}"><span class="card-icon">${icon}</span><p>${escapeHTML(label)}</p><strong>${value}</strong><small>${escapeHTML(hint)}</small></article>`;
}

function renderRecent(items) {
  const container = document.getElementById("recent-tasks");
  if (!items.length) {
    container.innerHTML = '<div class="empty-state"><div class="empty-icon">▶</div><h3>暂无任务</h3><p>创建备份策略或手动任务后将在这里展示。</p></div>';
    return;
  }
  container.innerHTML = items.map(item => `<div class="recent-item">
    <span class="task-symbol">${item.resource === "restore-tasks" ? "↶" : "▶"}</span>
    <div class="recent-main"><strong>${escapeHTML(item.name)}</strong><span>${item.resource === "restore-tasks" ? "恢复任务" : "备份任务"} · ${escapeHTML(item.step || "等待执行")}</span></div>
    <div><div class="progress-track"><i style="width:${clamp(item.percent || 0, 0, 100)}%"></i></div><span class="progress-label">${item.percent || 0}%</span></div>
    ${statusChip(item.phase || "Pending")}
  </div>`).join("");
}

function renderHealth(summaries) {
  const rows = [
    ["定时策略", summaries.policies, ["Ready", "Paused"]],
    ["备份任务", summaries["backup-tasks"], ["Completed"]],
    ["恢复点", summaries.records, ["Available", "PartiallyAvailable"]],
    ["恢复任务", summaries["restore-tasks"], ["Completed"]],
    ["全局配置", summaries.configs, ["Ready"]],
  ];
  document.getElementById("health-summary").innerHTML = rows.map(([name, value, healthy]) => {
    const good = phaseCount(value, healthy);
    const total = value?.total || 0;
    return `<div class="health-row"><div class="health-label"><strong>${name}</strong><span>${good} 个处于正常状态</span></div><span class="health-value">${total}</span></div>`;
  }).join("");
}

async function loadResources(resource, silent = false) {
  const loading = document.getElementById("table-loading");
  if (!silent) {
    loading.classList.remove("hidden");
    document.querySelector(".table-wrap").classList.add("hidden");
    document.getElementById("empty-state").classList.add("hidden");
  }
  try {
    const list = await api(`${API_BASE}/${resource}`);
    state.items = (list.items || []).sort((left, right) => {
      return new Date(path(right, "metadata.creationTimestamp", 0)).valueOf() - new Date(path(left, "metadata.creationTimestamp", 0)).valueOf();
    });
    if (["policies", "backup-tasks", "records"].includes(resource)) await loadPolicyRelations();
    document.getElementById("updated-at").textContent = `更新于 ${new Date().toLocaleTimeString("zh-CN", {hour12: false})}`;
    renderTable(resource);
  } catch (error) {
    toast("资源读取失败", error.message, true);
    state.items = [];
    renderTable(resource, error.message);
  } finally {
    loading.classList.add("hidden");
  }
}

function filterAndRender() { renderTable(state.currentView); }

function renderTable(resource, errorMessage = "") {
  const definition = resourceDefinitions[resource];
  if (!definition) return;
  const query = document.getElementById("search-input").value.trim().toLowerCase();
  state.filteredItems = state.items.filter(item => JSON.stringify({name: item.metadata?.name, spec: item.spec, status: item.status}).toLowerCase().includes(query));
  document.getElementById("resource-head").innerHTML = `<tr><th>名称</th>${definition.columns.map(([label]) => `<th>${escapeHTML(label)}</th>`).join("")}<th style="text-align:right">操作</th></tr>`;
  const body = document.getElementById("resource-body");
  body.innerHTML = state.filteredItems.map(item => {
    const name = item.metadata?.name || "—";
    const created = formatDate(item.metadata?.creationTimestamp);
    const cells = definition.columns.map(([, formatter]) => `<td>${formatter(item)}</td>`).join("");
    const actions = definition.actions(item).filter(a => a.id !== "view" || true).map(a => `<button class="action-button ${a.className}" data-action="${a.id}" data-name="${escapeAttribute(name)}">${escapeHTML(a.label)}</button>`).join("");
    return `<tr data-object-name="${escapeAttribute(name)}"><td><button class="text-button object-name" data-action="view" data-name="${escapeAttribute(name)}">${escapeHTML(name)}</button><span class="object-sub">创建于 ${created}</span></td>${cells}<td><div class="table-actions">${actions}</div></td></tr>`;
  }).join("");
  const empty = document.getElementById("empty-state");
  const table = document.querySelector(".table-wrap");
  empty.classList.toggle("hidden", state.filteredItems.length > 0);
  table.classList.toggle("hidden", state.filteredItems.length === 0);
  document.getElementById("empty-message").textContent = errorMessage || (query ? "没有匹配当前搜索条件的对象。" : `当前还没有${definition.singular}。`);
  document.getElementById("result-count").textContent = `${state.filteredItems.length} 个对象`;
}

async function handleTableAction(event) {
  const button = event.target.closest("[data-action]");
  if (!button) return;
  const actionID = button.dataset.action;
  const name = button.dataset.name;
  const object = state.items.find(item => item.metadata?.name === name);
  if (!object) return;
  switch (actionID) {
    case "view": return showDetail(state.currentView, object);
    case "edit": return openObjectWizard("edit", state.currentView, object);
    case "delete": return openDelete(state.currentView, object);
    case "restore": return openRestore(object);
    case "run":
      if (!window.confirm(`立即执行策略 ${name}？系统将创建一个独立的手动备份任务。`)) return;
      return performAction(state.currentView, name, "run", "备份任务已创建");
    case "suspend": return performAction(state.currentView, name, "suspend", "策略已停用");
    case "resume": return performAction(state.currentView, name, "resume", "策略已启用");
    case "refresh": return performAction(state.currentView, name, "refresh", "已触发重新检查");
    case "verify": return performAction(state.currentView, name, "verify", "已触发完整性校验");
    case "cancel":
      if (!window.confirm(`确认取消任务 ${name}？已执行的恢复操作不会自动回滚。`)) return;
      return performAction(state.currentView, name, "cancel", "取消请求已提交");
  }
}

async function performAction(resource, name, actionID, successMessage) {
  try {
    await api(`${API_BASE}/${resource}/${encodeURIComponent(name)}/actions/${actionID}`, {method: "POST"});
    toast("操作成功", successMessage);
    await loadResources(resource, true);
  } catch (error) { toast("操作失败", error.message, true); }
}

async function openCreate() {
  const resource = state.currentView;
  const definition = resourceDefinitions[resource];
  if (!definition?.creatable) return;
  try {
    const refs = await loadReferences();
    openEditor(resource, null, definition.template(refs));
  } catch (error) { toast("无法准备新建表单", error.message, true); }
}

async function loadReferences() {
  const [repositories, policies, records] = await Promise.all([
    api(`${API_BASE}/repositories`), api(`${API_BASE}/policies`), api(`${API_BASE}/records`),
  ]);
  const first = list => (list.items || [])[0]?.metadata?.name;
  return {repository: first(repositories), policy: first(policies), record: first(records)};
}

async function loadPolicyRelations() {
  const [tasks, records] = await Promise.all([api(`${API_BASE}/backup-tasks`), api(`${API_BASE}/records`)]);
  const relations = {};
  const ensure = name => relations[name] ||= {tasks: [], records: [], recordByTask: {}};
  (tasks.items || []).forEach(task => {
    const policyName = path(task, "spec.source.policyRef.name", "");
    if (policyName) ensure(policyName).tasks.push(task);
  });
  (records.items || []).forEach(record => {
    const policyName = path(record, "spec.policyRef.name", "");
    const taskName = path(record, "spec.sourceTaskRef.name", "");
    if (!policyName) return;
    const relation = ensure(policyName);
    relation.records.push(record);
    if (taskName) relation.recordByTask[taskName] = record;
  });
  Object.values(relations).forEach(relation => {
    const newestFirst = (left, right) => new Date(path(right, "metadata.creationTimestamp", 0)) - new Date(path(left, "metadata.creationTimestamp", 0));
    relation.tasks.sort(newestFirst);
    relation.records.sort(newestFirst);
  });
  state.policyRelations = relations;
}

function openEditor(resource, object = null, prepared = null) {
  const definition = resourceDefinitions[resource];
  const isEdit = Boolean(object);
  state.editor = {mode: isEdit ? "edit" : "create", resource, name: object?.metadata?.name || null};
  const value = prepared || cleanObject(object);
  document.getElementById("editor-eyebrow").textContent = isEdit ? "更新资源配置" : "创建 Kubernetes 对象";
  document.getElementById("editor-title").textContent = `${isEdit ? "编辑" : "新建"}${definition.singular}`;
  document.getElementById("object-editor").value = JSON.stringify(value, null, 2);
  document.getElementById("editor-error").classList.add("hidden");
  document.getElementById("save-button").disabled = false;
  document.getElementById("editor-dialog").showModal();
}

async function saveEditor(event) {
  if (event.submitter?.value === "cancel") return;
  event.preventDefault();
  const errorElement = document.getElementById("editor-error");
  let object;
  try { object = JSON.parse(document.getElementById("object-editor").value); }
  catch (error) {
    errorElement.textContent = `JSON 格式错误：${error.message}`;
    errorElement.classList.remove("hidden");
    return;
  }
  const {mode, resource, name} = state.editor;
  const saveButton = document.getElementById("save-button");
  saveButton.disabled = true;
  saveButton.textContent = "正在保存…";
  try {
    const endpoint = mode === "edit" ? `${API_BASE}/${resource}/${encodeURIComponent(name)}` : `${API_BASE}/${resource}`;
    await api(endpoint, {method: mode === "edit" ? "PUT" : "POST", body: JSON.stringify(object)});
    document.getElementById("editor-dialog").close();
    toast("保存成功", `${resourceDefinitions[resource].singular} ${object.metadata?.name || name} 已提交`);
    navigate(resource);
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.classList.remove("hidden");
  } finally {
    saveButton.disabled = false;
    saveButton.textContent = "保存";
  }
}

function showDetail(resource, object) {
  const definition = resourceDefinitions[resource];
  const detailKey = `${resource}/${path(object, "metadata.name", "")}/${path(object, "metadata.resourceVersion", "")}/${Date.now()}`;
  const presentation = detailPresentation(resource);
  state.detail = {resource, object, key: detailKey};
  document.getElementById("detail-kind").textContent = definition.kind;
  document.getElementById("detail-kind-icon").textContent = presentation.icon;
  document.getElementById("detail-title").textContent = object.metadata?.name || "—";
  document.getElementById("detail-subtitle").textContent = presentation.subtitle;
  document.getElementById("detail-status").innerHTML = statusChip(path(object, "status.phase", "Pending"));
  document.getElementById("detail-summary").innerHTML = renderDetailSummary(resource, object);
  document.getElementById("detail-content").innerHTML = renderObjectDetail(resource, object);
  document.getElementById("detail-json").textContent = JSON.stringify(object, null, 2);
  document.getElementById("detail-updated").textContent = `资源版本 ${path(object, "metadata.resourceVersion", "—")} · 创建于 ${formatDate(path(object, "metadata.creationTimestamp", null))}`;
  const message = document.getElementById("detail-message");
  const statusMessage = path(object, "status.message", "");
  const statusReason = path(object, "status.reason", "");
  message.className = `detail-message${detailMessageTone(path(object, "status.phase", ""))}`;
  message.textContent = [statusReason, statusMessage].filter(Boolean).join("：");
  message.classList.toggle("hidden", !message.textContent);
  const lineage = document.getElementById("detail-lineage");
  lineage.innerHTML = '<div class="lineage-loading"><span class="spinner"></span>正在读取策略、执行任务和恢复点关系…</div>';
  const relationTab = document.querySelector('[data-detail-tab="relations"]');
  relationTab.classList.toggle("hidden", resource === "configs");
  switchDetailTab("overview");
  document.getElementById("detail-edit").classList.toggle("hidden", !definition.editable);
  const dialog = document.getElementById("detail-dialog");
  if (!dialog.open) dialog.showModal();
  if (resource !== "configs") renderDetailLineage(resource, object, detailKey).catch(error => {
    if (state.detail?.key === detailKey) lineage.innerHTML = `<div class="wizard-error">${escapeHTML(error.message)}</div>`;
  });
}

function switchDetailTab(tabID) {
  document.querySelectorAll("[data-detail-tab]").forEach(tab => tab.classList.toggle("active", tab.dataset.detailTab === tabID));
  document.querySelectorAll("[data-detail-pane]").forEach(pane => pane.classList.toggle("hidden", pane.dataset.detailPane !== tabID));
}

function detailPresentation(resource) {
  return ({
    repositories: {icon: "▣", subtitle: "查看仓库连接、容量能力和数据保护配置"},
    policies: {icon: "◷", subtitle: "查看保护范围、调度规则、保留策略和执行历史"},
    "backup-tasks": {icon: "▶", subtitle: "查看单次备份的实时进度、固定范围和产出"},
    records: {icon: "◇", subtitle: "查看恢复点内容、完整性、快照和可恢复状态"},
    "restore-tasks": {icon: "↶", subtitle: "查看恢复计划、对象进度、冲突策略和执行结果"},
    configs: {icon: "⚙", subtitle: "查看 Operator 的并发、安全和运行参数"},
  })[resource] || {icon: "◇", subtitle: "查看 Kubernetes 对象详情"};
}

function detailMessageTone(phase) {
  if (["Failed", "Broken", "Invalid", "RepoUnavailable", "Cancelled"].includes(phase)) return " danger";
  if (["Degraded", "PartiallyFailed", "PartiallyAvailable", "SnapshotMissing", "Expired", "Paused"].includes(phase)) return " warning";
  return "";
}

function renderDetailSummary(resource, object) {
  const phase = path(object, "status.phase", "Pending");
  const summaries = {
    policies: [
      ["当前状态", statusChip(phase), true],
      ["下次执行", formatDate(path(object, "status.nextScheduleTime", null))],
      ["保护模式", path(object, "spec.selection.mode", "Namespace") === "Cluster" ? "整集群" : "命名空间"],
      ["活动任务", `${path(object, "status.activeTasks", []).length} 个`],
    ],
    "backup-tasks": [
      ["当前状态", statusChip(phase), true],
      ["完成进度", `${path(object, "status.progress.percent", 0)}%`],
      ["当前步骤", path(object, "status.step", "等待调度")],
      ["开始时间", formatDate(path(object, "status.startedAt", path(object, "metadata.creationTimestamp", null)))],
    ],
    records: [
      ["可用状态", statusChip(phase), true],
      ["是否可恢复", path(object, "status.restorable", false) ? "可以恢复" : "暂不可恢复"],
      ["备份大小", formatBytes(path(object, "spec.inventory.backupBytes", 0))],
      ["过期时间", formatDate(path(object, "spec.expiresAt", null))],
    ],
    "restore-tasks": [
      ["当前状态", statusChip(phase), true],
      ["恢复进度", `${restorePercent(object)}%`],
      ["当前步骤", path(object, "status.step", "等待调度")],
      ["恢复点", path(object, "spec.backupRecordRef.name", "—")],
    ],
    repositories: [
      ["当前状态", statusChip(phase), true],
      ["仓库类型", path(object, "spec.type", "—")],
      ["可用容量", path(object, "status.capacityKnown", false) ? formatBytes(path(object, "status.availableBytes", 0)) : "未知"],
      ["最近检查", formatDate(path(object, "status.lastCheckTime", null))],
    ],
    configs: [
      ["当前状态", statusChip(phase), true],
      ["Operator 版本", path(object, "status.operatorVersion", "—")],
      ["最近应用", formatDate(path(object, "status.lastAppliedAt", null))],
      ["日志级别", path(object, "spec.logLevel", "info")],
    ],
  };
  return (summaries[resource] || [
    ["当前状态", statusChip(phase), true],
    ["集群", path(object, "spec.clusterRef", state.clusterRef)],
    ["资源版本", path(object, "metadata.resourceVersion", "—")],
    ["创建时间", formatDate(path(object, "metadata.creationTimestamp", null))],
  ]).map(([label, value, html]) => `<div class="detail-cell"><span>${escapeHTML(label)}</span>${html ? value : `<strong>${escapeHTML(value)}</strong>`}</div>`).join("");
}

function renderObjectDetail(resource, object) {
  const renderer = ({
    repositories: renderRepositoryDetail,
    policies: renderPolicyDetail,
    "backup-tasks": renderBackupTaskDetail,
    records: renderRecordDetail,
    "restore-tasks": renderRestoreTaskDetail,
    configs: renderConfigDetail,
  })[resource];
  return `${renderer ? renderer(object) : ""}${renderConditionsSection(object)}${renderMetadataSection(object)}`;
}

function renderPolicyDetail(object) {
  const selection = path(object, "spec.selection", {});
  const preview = path(object, "status.selectionPreview", {});
  const pvc = path(selection, "pvc", {});
  return [
    detailSection("调度与执行", "策略何时运行以及并发任务的处理方式", detailList([
      detailRow("Cron 表达式", `<code>${escapeHTML(path(object, "spec.schedule.cron", "—"))}</code>`, true),
      detailRow("时区", path(object, "spec.schedule.timezone", "Etc/UTC")),
      detailRow("启用状态", detailToggle(path(object, "spec.enabled", false), "已启用", "已禁用"), true),
      detailRow("暂停调度", detailToggle(path(object, "spec.suspend", false), "已暂停", "运行中", true), true),
      detailRow("并发策略", path(object, "spec.concurrencyPolicy", "Forbid")),
      detailRow("漏跑策略", path(object, "spec.missedRunPolicy", "Skip")),
      detailRow("开始期限", path(object, "spec.startingDeadline", "1h")),
      detailRow("任务超时", path(object, "spec.timeout", "4h")),
    ])),
    detailSection("运行状态", "最近一次调度结果和连续失败情况", detailList([
      detailRow("最近调度", formatDate(path(object, "status.lastScheduleTime", null))),
      detailRow("最近成功", formatDate(path(object, "status.lastSuccessfulTime", null))),
      detailRow("下次执行", formatDate(path(object, "status.nextScheduleTime", null))),
      detailRow("连续失败", `${path(object, "status.consecutiveFailures", 0)} 次`),
      detailRow("活动任务", detailTags(path(object, "status.activeTasks", []).map(item => item.name), "无"), true),
      detailRow("仓库", detailObjectLink("repositories", path(object, "spec.repositoryRef.name", "")), true),
    ])),
    renderSelectionSection(selection, "保护范围", "策略内嵌的资源选择规则会固化到每次执行任务中"),
    detailSection("范围预估", "Operator 最近一次解析出的保护对象数量", detailMetrics([
      ["命名空间", path(preview, "namespaceCount", 0)], ["资源类型", path(preview, "resourceTypeCount", 0)],
      ["资源对象", path(preview, "resourceObjectCount", 0)], ["PVC", path(preview, "pvcCount", 0)],
      ["可快照 PVC", path(preview, "snapshotCapablePVCCount", 0)], ["不支持 PVC", path(preview, "unsupportedPVCCount", 0)],
      ["风险项", path(preview, "riskCount", 0)], ["生成时间", formatDate(path(preview, "generatedAt", null))],
    ]), {full: true}),
    detailSection("PVC 与一致性", "数据卷快照、应用一致性和失败处理", detailList([
      detailRow("PVC 快照", detailToggle(path(pvc, "enabled", false), "已启用", "未启用"), true),
      detailRow("快照类", path(pvc, "snapshotClassName", "自动选择")),
      detailRow("快照超时", path(pvc, "snapshotTimeout", "10m")),
      detailRow("失败策略", path(pvc, "failurePolicy", "ContinueAndMarkPartial")),
      detailRow("生命周期", path(pvc, "lifecycle", "RetainAfterRecordDeletion")),
      detailRow("一致性模式", path(selection, "consistencyMode", "CrashConsistent")),
      detailRow("前置 / 后置 Hook", `${path(selection, "hooks.pre", []).length} / ${path(selection, "hooks.post", []).length}`),
    ])),
    detailSection("保留与重试", "恢复点保留数量、期限与失败重试规则", detailList([
      detailRow("最多保留", `${path(object, "spec.retention.maxCopies", 7)} 份`),
      detailRow("最少保留", `${path(object, "spec.retention.minCopies", 1)} 份`),
      detailRow("最长保留", `${path(object, "spec.retention.maxAgeDays", 30)} 天`),
      detailRow("同步删除快照", detailToggle(path(object, "spec.retention.deleteSnapshots", false), "是", "否", true), true),
      detailRow("最多尝试", `${path(object, "spec.retryPolicy.maxAttempts", 3)} 次`),
      detailRow("退避时间", `${path(object, "spec.retryPolicy.backoff", "30s")} → ${path(object, "spec.retryPolicy.maxBackoff", "10m")}`),
    ])),
  ].join("");
}

function renderSelectionSection(selection, title, description) {
  const resources = path(selection, "resources", {});
  return detailSection(title, description, detailList([
    detailRow("选择模式", path(selection, "mode", "Namespace") === "Cluster" ? "整集群" : "命名空间"),
    detailRow("包含命名空间", detailTags(path(selection, "includeNamespaces", []), "全部"), true),
    detailRow("排除命名空间", detailTags(path(selection, "excludeNamespaces", []), "无"), true),
    detailRow("包含资源", detailTags(path(resources, "include", []), "全部"), true),
    detailRow("排除资源", detailTags(path(resources, "exclude", []), "无"), true),
    detailRow("标签选择器", `<code>${escapeHTML(formatSelector(path(selection, "labelSelector", null)))}</code>`, true),
    detailRow("附加内容", detailTags([
      path(selection, "includeClusterResources", false) ? "集群资源" : "",
      path(selection, "includeSecrets", false) ? "Secret" : "",
      path(selection, "includeCRDs", false) ? "CRD" : "",
      path(selection, "includeCustomResources", false) ? "自定义资源" : "",
    ].filter(Boolean), "仅基础资源"), true),
  ]), {full: true});
}

function renderBackupTaskDetail(object) {
  const progress = path(object, "status.progress", {});
  const selection = path(object, "spec.backupSpec.selection", null);
  const sourceType = path(object, "spec.source.type", "OneTime");
  return [
    detailSection("执行进度", "单次备份任务的实时处理量和数据吞吐", `${detailMetrics([
      ["总体进度", `${path(progress, "percent", 0)}%`], ["资源总数", path(progress, "totalResources", 0)],
      ["已处理", path(progress, "processedResources", 0)], ["失败资源", path(progress, "failedResources", 0)],
      ["PVC 总数", path(progress, "totalPVCs", 0)], ["成功快照", path(progress, "succeededSnapshots", 0)],
      ["已处理数据", formatBytes(path(progress, "bytesProcessed", 0))], ["已上传数据", formatBytes(path(progress, "bytesUploaded", 0))],
    ])}${detailProgress(path(progress, "percent", 0), path(object, "status.step", "等待执行"))}`, {full: true}),
    detailSection("来源与执行环境", "任务来源、冻结配置和实际执行位置", detailList([
      detailRow("任务类型", sourceType === "Policy" ? "策略执行" : "一次性备份"),
      ...(sourceType === "Policy" ? [detailRow("来源策略", detailObjectLink("policies", path(object, "spec.source.policyRef.name", "")), true)] : []),
      detailRow("备份仓库", detailObjectLink("repositories", path(object, "spec.backupSpec.repositoryRef.name", "")), true),
      detailRow("触发方式", path(object, "spec.trigger", "Manual")),
      detailRow("计划时间", formatDate(path(object, "spec.scheduledAt", null))),
      detailRow("策略版本", path(object, "spec.policyGeneration", "—")),
      detailRow("配置哈希", `<code>${escapeHTML(shortHash(path(object, "spec.backupSpecHash", "—")))}</code>`, true),
      detailRow("执行 Worker", path(object, "status.workerName", "—")),
      detailRow("执行节点", path(object, "status.executionNode", "—")),
      detailRow("最近心跳", formatDate(path(object, "status.lastHeartbeatTime", null))),
    ])),
    detailSection("产出与归档", "备份归档、校验和以及最终恢复点", detailList([
      detailRow("备份 ID", path(object, "status.backupID", "—")),
      detailRow("备份大小", formatBytes(path(object, "status.backupBytes", 0))),
      detailRow("归档路径", `<code>${escapeHTML(path(object, "status.archivePath", "—"))}</code>`, true),
      detailRow("归档校验和", `<code>${escapeHTML(shortHash(path(object, "status.archiveChecksum", "—")))}</code>`, true),
      detailRow("恢复点", detailObjectLink("records", path(object, "status.recordRef.name", "")), true),
      detailRow("告警数量", `${path(object, "status.warnings", 0)} 个`),
      detailRow("执行尝试", `${path(object, "status.attempt", 0)} 次`),
      detailRow("允许部分恢复点", detailToggle(path(object, "spec.backupSpec.allowPartialRecord", false), "允许", "不允许"), true),
    ])),
    selection ? renderSelectionSection(selection, "冻结的执行范围", "任务创建或解析时固化，后续策略修改不会影响本次执行") : detailSection("冻结的执行范围", "策略任务进入执行前会固化完整配置", '<div class="detail-empty">当前任务尚未解析策略配置。</div>', {full: true}),
    renderCheckpointSection(path(object, "status.checkpoints", [])),
    renderSnapshotSection(path(object, "status.snapshots", [])),
    renderErrorSection(path(object, "status.errors", [])),
  ].join("");
}

function renderRecordDetail(object) {
  const inventory = path(object, "spec.inventory", {});
  return [
    detailSection("备份内容清单", "该恢复点实际包含的资源、命名空间和数据卷", detailMetrics([
      ["资源对象", path(inventory, "resourceCount", 0)], ["命名空间", path(inventory, "namespaceCount", 0)],
      ["PVC", path(inventory, "pvcCount", 0)], ["快照", path(inventory, "snapshotCount", 0)],
      ["失败资源", path(inventory, "failedResourceCount", 0)], ["失败快照", path(inventory, "failedSnapshotCount", 0)],
      ["备份大小", formatBytes(path(inventory, "backupBytes", 0))], ["完整性", path(object, "spec.contentCompleteness", "Unknown")],
    ]), {full: true}),
    detailSection("来源链路", "恢复点的创建方式、执行任务和仓库", detailList([
      detailRow("来源类型", path(object, "spec.sourceType", "OneTime") === "Policy" ? "备份策略" : "一次性备份"),
      ...(path(object, "spec.sourceType", "OneTime") === "Policy" ? [detailRow("来源策略", detailObjectLink("policies", path(object, "spec.policyRef.name", "")), true)] : []),
      detailRow("执行任务", detailObjectLink("backup-tasks", path(object, "spec.sourceTaskRef.name", "")), true),
      detailRow("备份仓库", detailObjectLink("repositories", path(object, "spec.repositoryRef.name", "")), true),
      detailRow("来源集群", path(object, "spec.source.clusterRef", "—")),
      detailRow("Kubernetes", path(object, "spec.source.kubernetesVersion", "—")),
      detailRow("范围模式", path(object, "spec.source.scopeMode", "—")),
      detailRow("命名空间", detailTags(path(object, "spec.source.namespaces", []), "全部"), true),
    ])),
    detailSection("完整性与安全", "归档位置、校验结果、加密和保护状态", detailList([
      detailRow("备份 ID", path(object, "spec.backupID", "—")),
      detailRow("备份路径", `<code>${escapeHTML(path(object, "spec.backupPath", "—"))}</code>`, true),
      detailRow("校验算法", path(object, "spec.checksumAlgorithm", "—")),
      detailRow("校验和", `<code>${escapeHTML(shortHash(path(object, "spec.checksum", "—")))}</code>`, true),
      detailRow("格式版本", path(object, "spec.formatVersion", "—")),
      detailRow("配置哈希", `<code>${escapeHTML(shortHash(path(object, "spec.backupSpecHash", "—")))}</code>`, true),
      detailRow("加密", detailToggle(path(object, "spec.encryption.enabled", false), path(object, "spec.encryption.algorithm", "已加密"), "未加密"), true),
      detailRow("已验证文件", `${path(object, "status.verifiedFiles", 0)} 个`),
      detailRow("最近校验", formatDate(path(object, "status.lastVerifiedAt", null))),
      detailRow("删除保护", detailToggle(path(object, "status.protected", false), "受保护", "未保护"), true),
      detailRow("历史恢复", `${path(object, "status.restoreCount", 0)} 次`),
    ])),
    renderSnapshotSection(path(object, "spec.snapshots", [])),
    path(object, "status.missingSnapshots", []).length ? detailSection("缺失快照", "恢复前需要处理的快照引用", detailTags(path(object, "status.missingSnapshots", [])), {full: true}) : "",
  ].join("");
}

function renderRestoreTaskDetail(object) {
  const progress = path(object, "status.progress", {});
  const plan = path(object, "status.plan", {});
  const total = path(progress, "total", 0);
  const percent = restorePercent(object);
  return [
    detailSection("恢复进度", "对象与 PVC 的实际处理结果", `${detailMetrics([
      ["总体进度", `${percent}%`], ["对象总数", total], ["已处理", path(progress, "processed", 0)], ["已创建", path(progress, "created", 0)],
      ["已更新", path(progress, "updated", 0)], ["已跳过", path(progress, "skipped", 0)], ["失败对象", path(progress, "failed", 0)], ["PVC 就绪", `${path(progress, "boundPVCs", 0)} / ${path(progress, "totalPVCs", 0)}`],
    ])}${detailProgress(percent, path(object, "status.step", "等待执行"))}`, {full: true}),
    detailSection("恢复来源与目标", "恢复使用的恢复点以及目标集群", detailList([
      detailRow("恢复点", detailObjectLink("records", path(object, "spec.backupRecordRef.name", "")), true),
      detailRow("目标集群", path(object, "spec.targetClusterRef", "—")),
      detailRow("触发方式", path(object, "spec.trigger", "Manual")),
      detailRow("恢复模式", path(object, "spec.mode", "Original")),
      detailRow("Dry Run", detailToggle(path(object, "spec.dryRun", false), "仅预检", "实际执行"), true),
      detailRow("恢复 PVC", detailToggle(path(object, "spec.restorePVC", false), "恢复", "不恢复"), true),
      detailRow("仅元数据", detailToggle(path(object, "spec.metadataOnly", false), "是", "否", true), true),
      detailRow("任务超时", path(object, "spec.timeout", "4h")),
    ])),
    detailSection("冲突与资源规则", "对象筛选、命名空间映射和冲突处理", detailList([
      detailRow("包含资源", detailTags(path(object, "spec.resourceSelection.include", []), "全部"), true),
      detailRow("排除资源", detailTags(path(object, "spec.resourceSelection.exclude", []), "无"), true),
      detailRow("集群资源", detailToggle(path(object, "spec.resourceSelection.includeClusterResources", false), "包含", "不包含"), true),
      detailRow("默认冲突策略", path(object, "spec.conflictPolicy.default", "Skip")),
      detailRow("允许重建", detailToggle(path(object, "spec.conflictPolicy.allowRecreate", false), "允许", "不允许"), true),
      detailRow("高风险确认", detailToggle(path(object, "spec.conflictPolicy.highRiskConfirmed", false), "已确认", "未确认"), true),
      detailRow("命名空间映射", detailMap(path(object, "spec.namespaceMapping", {})), true),
      detailRow("存储类映射", detailMap(path(object, "spec.storageClassMapping", {})), true),
    ])),
    detailSection("恢复计划", "执行前生成的对象计划与风险统计", detailMetrics([
      ["计划对象", path(plan, "totalObjects", 0)], ["计划 PVC", path(plan, "totalPVCs", 0)],
      ["冲突", path(plan, "conflictCount", 0)], ["阻塞项", path(plan, "blockingCount", 0)],
      ["告警", path(plan, "warningCount", 0)], ["生成时间", formatDate(path(plan, "generatedAt", null))],
      ["计划哈希", shortHash(path(plan, "hash", "—"))], ["计划引用", path(plan, "reference", "—")],
    ]), {full: true}),
    renderCheckpointSection(path(object, "status.checkpoints", [])),
    renderErrorSection(path(object, "status.errors", [])),
  ].join("");
}

function renderRepositoryDetail(object) {
  const local = path(object, "spec.local", {});
  const sftp = path(object, "spec.sftp", {});
  const capabilities = path(object, "status.capabilities", {});
  return [
    detailSection("连接与存储", "仓库位置、运行节点和健康检查配置", detailList([
      detailRow("仓库类型", path(object, "spec.type", "—")),
      detailRow("启用状态", detailToggle(path(object, "spec.enabled", false), "已启用", "已禁用"), true),
      detailRow("本地模式", path(local, "mode", "—")),
      detailRow("本地路径", `<code>${escapeHTML(path(local, "path", "—"))}</code>`, true),
      detailRow("指定节点", path(local, "nodeName", path(object, "status.resolvedNodeName", "—"))),
      detailRow("SFTP 地址", path(sftp, "host", "—")),
      detailRow("健康间隔", path(object, "spec.healthCheckInterval", "30m")),
      detailRow("请求超时", path(object, "spec.timeout", "30s")),
      detailRow("最近成功检查", formatDate(path(object, "status.lastSuccessfulCheckTime", null))),
    ])),
    detailSection("容量与能力", "仓库可用空间和支持的存储操作", `${detailMetrics([
      ["总容量", path(object, "status.capacityKnown", false) ? formatBytes(path(object, "status.totalBytes", 0)) : "未知"],
      ["可用容量", path(object, "status.capacityKnown", false) ? formatBytes(path(object, "status.availableBytes", 0)) : "未知"],
      ["关联策略", path(object, "status.activePolicyCount", 0)], ["恢复点", path(object, "status.recordCount", 0)],
    ])}<div class="detail-progress">${detailTags([capabilities.read ? "读取" : "", capabilities.write ? "写入" : "", capabilities.delete ? "删除" : "", capabilities.atomicRename ? "原子重命名" : "", capabilities.capacity ? "容量探测" : ""].filter(Boolean), "能力尚未探测")}</div>`),
    detailSection("数据保护", "压缩、加密和删除保护参数", detailList([
      detailRow("压缩算法", path(object, "spec.compression.algorithm", "—")),
      detailRow("压缩级别", path(object, "spec.compression.level", "—")),
      detailRow("仓库加密", detailToggle(path(object, "spec.encryption.enabled", false), path(object, "spec.encryption.algorithm", "已加密"), "未加密"), true),
      detailRow("删除保护", detailToggle(path(object, "spec.deletionProtection", false), "已启用", "未启用"), true),
      detailRow("最小可用空间", path(object, "spec.minimumFreeBytes", "—")),
      detailRow("失败重试", `${path(object, "spec.retryCount", 3)} 次`),
    ]), {full: true}),
  ].join("");
}

function renderConfigDetail(object) {
  return [
    detailSection("并发限制", "Operator 同时处理不同类型任务的上限", detailMetrics([
      ["备份任务", path(object, "spec.concurrency.maxBackupTasks", 3)], ["恢复任务", path(object, "spec.concurrency.maxRestoreTasks", 1)],
      ["单任务快照", path(object, "spec.concurrency.maxSnapshotsPerTask", 10)], ["仓库操作", path(object, "spec.concurrency.maxRepositoryOperations", 4)],
    ]), {full: true}),
    detailSection("默认参数", "新任务和控制器使用的默认时区与超时", detailList([
      detailRow("默认时区", path(object, "spec.defaultTimezone", "Etc/UTC")),
      detailRow("备份超时", path(object, "spec.defaultBackupTimeout", "4h")),
      detailRow("恢复超时", path(object, "spec.defaultRestoreTimeout", "4h")),
      detailRow("快照超时", path(object, "spec.defaultSnapshotTimeout", "10m")),
      detailRow("工作目录", `<code>${escapeHTML(path(object, "spec.workspacePath", "—"))}</code>`, true),
      detailRow("配置哈希", `<code>${escapeHTML(shortHash(path(object, "status.effectiveConfigHash", "—")))}</code>`, true),
    ])),
    detailSection("API 与回收", "Kubernetes 客户端限流和垃圾回收周期", detailList([
      detailRow("API QPS", path(object, "spec.kubernetesClient.qps", 20)),
      detailRow("Burst", path(object, "spec.kubernetesClient.burst", 40)),
      detailRow("分页大小", path(object, "spec.kubernetesClient.pageSize", 500)),
      detailRow("回收周期", path(object, "spec.garbageCollection.interval", "1h")),
      detailRow("暂存宽限期", path(object, "spec.garbageCollection.stagingGracePeriod", "24h")),
      detailRow("终态任务保留", `${path(object, "spec.garbageCollection.terminalTaskTTLDays", 90)} 天`),
    ])),
    detailSection("安全策略", "Secret、SFTP 和 Hook 执行边界", detailList([
      detailRow("Secret 命名空间", detailTags(path(object, "spec.security.allowedSecretNamespaces", []), "未限制"), true),
      detailRow("Secret 必须加密", detailToggle(path(object, "spec.security.requireEncryptionForSecrets", false), "是", "否", true), true),
      detailRow("允许不安全 SFTP", detailToggle(path(object, "spec.security.allowInsecureSFTP", false), "允许", "禁止", true), true),
      detailRow("允许执行 Hook", detailToggle(path(object, "spec.security.hookExecutionEnabled", false), "允许", "禁止", true), true),
    ]), {full: true}),
  ].join("");
}

function detailSection(title, description, content, options = {}) {
  const count = options.count === undefined ? "" : `<span class="detail-section-count">${escapeHTML(options.count)}</span>`;
  return `<section class="detail-section${options.full ? " full" : ""}"><header class="detail-section-header"><div><h3>${escapeHTML(title)}</h3><p>${escapeHTML(description)}</p></div>${count}</header>${content}</section>`;
}

function detailRow(label, value, html = false) { return {label, value, html}; }

function detailList(rows) {
  return `<dl class="detail-list">${rows.map(row => `<div class="detail-list-row"><dt>${escapeHTML(row.label)}</dt><dd>${row.html ? row.value : escapeHTML(row.value ?? "—")}</dd></div>`).join("")}</dl>`;
}

function detailMetrics(items) {
  return `<div class="detail-metrics">${items.map(([label, value, hint]) => `<div class="detail-metric"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value ?? "—")}</strong>${hint ? `<small>${escapeHTML(hint)}</small>` : ""}</div>`).join("")}</div>`;
}

function detailProgress(percent, label) {
  const value = clamp(percent, 0, 100);
  return `<div class="detail-progress"><div class="detail-progress-heading"><span>${escapeHTML(label || "处理中")}</span><strong>${value}%</strong></div><div class="progress-track"><i style="width:${value}%"></i></div></div>`;
}

function detailTags(items, emptyText = "无") {
  const values = (Array.isArray(items) ? items : []).filter(value => value !== undefined && value !== null && value !== "");
  if (!values.length) return `<span class="muted-text">${escapeHTML(emptyText)}</span>`;
  return `<span class="detail-tags">${values.map(value => `<span class="detail-tag">${escapeHTML(value)}</span>`).join("")}</span>`;
}

function detailToggle(value, truthy, falsy, dangerWhenTrue = false) {
  return `<span class="detail-tag ${value ? (dangerWhenTrue ? "negative" : "positive") : ""}">${escapeHTML(value ? truthy : falsy)}</span>`;
}

function detailMap(value) {
  const entries = Object.entries(value || {});
  return entries.length ? detailTags(entries.map(([from, to]) => `${from} → ${to}`)) : '<span class="muted-text">无映射</span>';
}

function detailObjectLink(resource, name) {
  if (!name) return '<span class="muted-text">—</span>';
  return `<button class="object-link" data-related-resource="${escapeAttribute(resource)}" data-related-name="${escapeAttribute(name)}">${escapeHTML(name)}</button>`;
}

function renderCheckpointSection(checkpoints) {
  if (!checkpoints.length) return "";
  return detailSection("执行检查点", "控制器记录的可恢复执行步骤", `<div class="checkpoint-list">${checkpoints.map(item => `<div class="checkpoint-row"><div class="event-copy"><strong>${escapeHTML(path(item, "step", "—"))}</strong><span>${escapeHTML(path(item, "key", "—"))}</span></div><span class="condition-reason">${escapeHTML(path(item, "externalID", "无外部标识"))}</span><span class="condition-state ${path(item, "completed", false) ? "complete" : ""}">${path(item, "completed", false) ? "已完成" : "进行中"}</span></div>`).join("")}</div>`, {full: true, count: `${checkpoints.length} 项`});
}

function renderSnapshotSection(snapshots) {
  if (!snapshots.length) return "";
  return detailSection("PVC 快照", "数据卷快照及其就绪状态", `<div class="snapshot-list">${snapshots.map(item => `<div class="snapshot-row"><div class="event-copy"><strong>${escapeHTML(`${path(item, "pvcNamespace", "default")}/${path(item, "pvcName", "—")}`)}</strong><span>${escapeHTML(path(item, "volumeSnapshotName", "未创建 VolumeSnapshot"))}</span></div><span class="condition-reason">${escapeHTML(`${path(item, "driver", "—")} · ${formatBytes(path(item, "restoreSize", 0))}`)}</span><span class="condition-state ${path(item, "readyToUse", false) ? "complete" : path(item, "error", "") ? "failed" : ""}">${path(item, "readyToUse", false) ? "可用" : path(item, "phase", "等待")}</span></div>`).join("")}</div>`, {full: true, count: `${snapshots.length} 个`});
}

function renderErrorSection(errors) {
  if (!errors.length) return "";
  return detailSection("错误明细", "任务执行过程中记录的结构化错误", `<div class="error-list">${errors.map(item => `<div class="error-row"><div class="event-copy"><strong>${escapeHTML(path(item, "code", "Unknown"))}</strong><span>${formatDate(path(item, "at", null))}</span></div><span class="condition-reason">${escapeHTML(path(item, "message", "—"))}</span><span class="condition-state ${path(item, "retryable", false) ? "" : "failed"}">${path(item, "retryable", false) ? "可重试" : "不可重试"}</span></div>`).join("")}</div>`, {full: true, count: `${errors.length} 项`});
}

function renderConditionsSection(object) {
  const conditions = path(object, "status.conditions", []);
  const content = conditions.length ? `<div class="condition-list">${conditions.map(condition => `<div class="condition-row"><div class="condition-name"><strong>${escapeHTML(path(condition, "type", "Unknown"))}</strong><span>${formatDate(path(condition, "lastTransitionTime", null))}</span></div><span class="condition-reason">${escapeHTML([path(condition, "reason", ""), path(condition, "message", "")].filter(Boolean).join(" · ") || "无补充信息")}</span><span class="condition-state ${String(path(condition, "status", "Unknown")).toLowerCase()}">${escapeHTML(path(condition, "status", "Unknown"))}</span></div>`).join("")}</div>` : '<div class="detail-empty">Operator 尚未写入状态条件。</div>';
  return detailSection("状态条件", "Kubernetes Conditions 反映对象当前是否就绪以及状态变化原因", content, {full: true, count: `${conditions.length} 项`});
}

function renderMetadataSection(object) {
  const labels = Object.entries(path(object, "metadata.labels", {})).map(([key, value]) => `${key}=${value}`);
  const annotations = Object.entries(path(object, "metadata.annotations", {})).map(([key, value]) => `${key}=${value}`);
  return detailSection("Kubernetes 元数据", "用于并发控制、归属识别和生命周期管理", detailList([
    detailRow("UID", `<code>${escapeHTML(path(object, "metadata.uid", "—"))}</code>`, true),
    detailRow("Generation", path(object, "metadata.generation", "—")),
    detailRow("Observed Generation", path(object, "status.observedGeneration", "—")),
    detailRow("Resource Version", path(object, "metadata.resourceVersion", "—")),
    detailRow("Labels", detailTags(labels, "无"), true),
    detailRow("Annotations", detailTags(annotations, "无"), true),
    detailRow("Finalizers", detailTags(path(object, "metadata.finalizers", []), "无"), true),
  ]), {full: true});
}

function formatSelector(selector) {
  if (!selector) return "无";
  return JSON.stringify(selector);
}

function shortHash(value) {
  const text = String(value || "—");
  return text.length > 24 ? `${text.slice(0, 12)}…${text.slice(-8)}` : text;
}

function restorePercent(object) {
  const total = path(object, "status.progress.total", 0);
  return total ? Math.round(path(object, "status.progress.processed", 0) * 100 / total) : 0;
}

async function renderDetailLineage(resource, object, detailKey) {
  const container = document.getElementById("detail-lineage");
  const commit = html => { if (state.detail?.key === detailKey) container.innerHTML = html; };
  if (resource === "policies") {
    const response = await api(`/api/policy-runs/${encodeURIComponent(object.metadata.name)}`);
    const runs = response.runs || [];
    commit(`<div class="lineage-header"><div><strong>运行与恢复点</strong><span>策略配置、单次执行和可恢复资产分别展示状态</span></div><span>${runs.length} 次执行</span></div>${runs.length ? `<div class="run-list">${runs.slice(0, 8).map(renderPolicyRun).join("")}</div>` : '<div class="lineage-empty">该策略尚未产生执行任务。</div>'}`);
    return;
  }
  if (resource === "repositories") {
    const [policiesResponse, recordsResponse] = await Promise.all([api(`${API_BASE}/policies`), api(`${API_BASE}/records`)]);
    const repositoryName = path(object, "metadata.name", "");
    const policies = (policiesResponse.items || []).filter(item => path(item, "spec.repositoryRef.name", "") === repositoryName);
    const records = (recordsResponse.items || []).filter(item => path(item, "spec.repositoryRef.name", "") === repositoryName);
    commit(`<div class="lineage-header"><div><strong>仓库引用</strong><span>使用该仓库的策略与已经落盘的恢复点</span></div><span>${policies.length} 个策略 · ${records.length} 个恢复点</span></div>${policies.length || records.length ? `<div class="related-grid">${policies.map(item => relationNode("policies", item, "备份策略", path(item, "status.phase", "Unknown"))).join("")}${records.slice(0, 8).map(item => relationNode("records", item, "恢复点", path(item, "status.phase", "Unknown"))).join("")}</div>` : '<div class="lineage-empty">当前仓库尚未被策略或恢复点引用。</div>'}`);
    return;
  }
  if (resource === "restore-tasks") {
    const recordName = path(object, "spec.backupRecordRef.name", "");
    const record = recordName ? await api(`${API_BASE}/records/${encodeURIComponent(recordName)}`).catch(() => null) : null;
    const policyName = path(record, "spec.policyRef.name", "");
    const taskName = path(record, "spec.sourceTaskRef.name", "");
    const [policy, task] = await Promise.all([
      policyName ? api(`${API_BASE}/policies/${encodeURIComponent(policyName)}`).catch(() => null) : null,
      taskName ? api(`${API_BASE}/backup-tasks/${encodeURIComponent(taskName)}`).catch(() => null) : null,
    ]);
    const sourceNode = policy ? relationNode("policies", policy, "备份策略", path(policy, "status.phase", "Unknown")) : '<div class="lineage-node"><span>备份来源</span><strong>一次性备份</strong><small>任务内联完整执行配置</small></div>';
    commit(`<div class="lineage-header"><div><strong>完整备份与恢复链</strong><span>从备份来源追溯到本次恢复执行</span></div></div><div class="lineage-chain four">
      ${sourceNode}<span class="lineage-arrow">→</span>
      ${relationNode("backup-tasks", task, "备份任务", path(task, "status.phase", "Unknown"))}<span class="lineage-arrow">→</span>
      ${relationNode("records", record, "恢复点", path(record, "status.phase", "Unknown"))}<span class="lineage-arrow">→</span>
      ${relationNode("restore-tasks", object, "恢复任务", path(object, "status.phase", "Unknown"))}
    </div>`);
    return;
  }
  const policyName = resource === "backup-tasks" ? path(object, "spec.source.policyRef.name", "") : path(object, "spec.policyRef.name", "");
  const taskName = resource === "backup-tasks" ? object.metadata.name : path(object, "spec.sourceTaskRef.name", "");
  const recordName = resource === "records" ? object.metadata.name : path(object, "status.recordRef.name", "");
  const policy = policyName ? await api(`${API_BASE}/policies/${encodeURIComponent(policyName)}`).catch(() => null) : null;
  const task = resource === "backup-tasks" ? object : taskName ? await api(`${API_BASE}/backup-tasks/${encodeURIComponent(taskName)}`).catch(() => null) : null;
  const record = resource === "records" ? object : recordName ? await api(`${API_BASE}/records/${encodeURIComponent(recordName)}`).catch(() => null) : null;
  const missingRecord = task && !record && terminalPhases.has(path(task, "status.phase", ""));
  const sourceNode = policy ? relationNode("policies", policy, "备份策略", path(policy, "status.phase", "Unknown")) : '<div class="lineage-node"><span>备份来源</span><strong>一次性备份</strong><small>任务自身定义备份范围</small></div>';
  commit(`<div class="lineage-header"><div><strong>备份来源链</strong><span>来源定义范围，任务记录执行，恢复点决定是否可恢复</span></div></div><div class="lineage-chain">
    ${sourceNode}
    <span class="lineage-arrow">→</span>
    ${relationNode("backup-tasks", task, "执行任务", path(task, "status.phase", "Unknown"))}
    <span class="lineage-arrow">→</span>
    ${record ? relationNode("records", record, "恢复点", path(record, "status.phase", "Unknown")) : `<div class="lineage-node missing"><span>恢复点</span><strong>${missingRecord ? "未生成" : "等待生成"}</strong><small>${missingRecord ? "本次执行没有形成可恢复资产" : "任务完成后将生成并校验"}</small></div>`}
  </div>`);
}

function renderPolicyRun(run) {
  const task = run.task || {};
  const record = run.record || null;
  return `<div class="run-row">
    <button class="run-object" data-related-resource="backup-tasks" data-related-name="${escapeAttribute(path(task, "metadata.name", ""))}"><strong>${escapeHTML(path(task, "metadata.name", "—"))}</strong><span>${escapeHTML(path(task, "spec.trigger", "Manual"))} · ${formatDate(path(task, "metadata.creationTimestamp", null))}</span></button>
    ${statusChip(path(task, "status.phase", "Pending"))}<span class="run-arrow">→</span>
    ${record ? `<button class="run-object" data-related-resource="records" data-related-name="${escapeAttribute(path(record, "metadata.name", ""))}"><strong>${escapeHTML(path(record, "metadata.name", "—"))}</strong><span>${escapeHTML(run.conclusion || "恢复点")}</span></button>${statusChip(path(record, "status.phase", "Pending"))}` : `<div class="run-object muted"><strong>未生成恢复点</strong><span>${escapeHTML(run.conclusion || "任务尚未完成")}</span></div><span>—</span>`}
  </div>`;
}

function relationNode(resource, object, label, phase) {
  if (!object) return `<div class="lineage-node missing"><span>${label}</span><strong>不可用</strong></div>`;
  return `<button class="lineage-node" data-related-resource="${resource}" data-related-name="${escapeAttribute(path(object, "metadata.name", ""))}"><span>${label}</span><strong>${escapeHTML(path(object, "metadata.name", "—"))}</strong><small>${statusChip(phase)}</small></button>`;
}

function openRestore(record) {
  const namespaces = path(record, "spec.source.namespaces", []);
  openEditor("restore-tasks", null, restoreTemplate(record.metadata.name, namespaces));
}

function restoreTemplate(recordName, namespaces) {
  const mapping = {};
  namespaces.forEach(namespace => { mapping[namespace] = `${namespace}-restored`; });
  return baseObject("RestoreTask", `restore-${compactTimestamp()}`, {
    clusterRef: state.clusterRef, trigger: "Manual", backupRecordRef: {name: recordName}, targetClusterRef: state.clusterRef,
    mode: namespaces.length ? "Mapping" : "Original", namespaceMapping: mapping,
    resourceSelection: {include: ["*"], includeClusterResources: false}, restorePVC: false, metadataOnly: false,
    storageClassMapping: {}, conflictPolicy: {default: "Skip", allowRecreate: false, highRiskConfirmed: false},
    dryRun: true, failurePolicy: "Continue", timeout: "4h",
  });
}

function openDelete(resource, object) {
  state.deletion = {resource, object};
  const name = object.metadata?.name || "";
  document.getElementById("delete-name").textContent = name;
  document.getElementById("delete-confirmation").value = "";
  document.getElementById("force-delete").checked = false;
  document.getElementById("record-delete-options").classList.toggle("hidden", resource !== "records");
  document.getElementById("delete-error").classList.add("hidden");
  document.getElementById("delete-dialog").showModal();
}

async function submitDelete(event) {
  if (event.submitter?.value === "cancel") return;
  event.preventDefault();
  const deletion = state.deletion;
  if (!deletion) return;
  const name = deletion.object.metadata?.name || "";
  const errorElement = document.getElementById("delete-error");
  if (document.getElementById("delete-confirmation").value !== name) {
    errorElement.textContent = "输入的对象名称不一致";
    errorElement.classList.remove("hidden");
    return;
  }
  const params = new URLSearchParams();
  if (deletion.resource === "records") params.set("mode", document.getElementById("delete-mode").value);
  if (document.getElementById("force-delete").checked) params.set("force", "true");
  try {
    await api(`${API_BASE}/${deletion.resource}/${encodeURIComponent(name)}?${params}`, {method: "DELETE"});
    document.getElementById("delete-dialog").close();
    toast("删除请求已提交", `${name} 正在由 Operator 安全清理`);
    await loadResources(deletion.resource, true);
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.classList.remove("hidden");
  }
}

async function api(url, options = {}) {
  const response = await fetch(url, {headers: {"Content-Type": "application/json"}, ...options});
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error?.message || `请求失败（HTTP ${response.status}）`);
  return payload;
}

function baseObject(kind, name, spec) {
  return {apiVersion: "protection.platform.io/v1alpha1", kind, metadata: {name}, spec};
}

function cleanObject(object) {
  const annotations = {...(object.metadata?.annotations || {})};
  delete annotations["kubectl.kubernetes.io/last-applied-configuration"];
  delete annotations["protection.platform.io/ui-refresh-requested-at"];
  return {
    apiVersion: object.apiVersion,
    kind: object.kind,
    metadata: {
      name: object.metadata?.name,
      ...(object.metadata?.labels ? {labels: object.metadata.labels} : {}),
      ...(Object.keys(annotations).length ? {annotations} : {}),
    },
    spec: object.spec || {},
  };
}

function action(id, label, className = "") { return {id, label, className}; }

function path(object, dottedPath, fallback = undefined) {
  const result = dottedPath.split(".").reduce((value, key) => value == null ? undefined : value[key], object);
  return result === undefined || result === null || result === "" ? fallback : result;
}

function statusChip(phase) {
  const success = ["Ready", "Available", "Completed"].includes(phase);
  const warning = ["Paused", "PartiallyAvailable", "PartiallyFailed", "Degraded", "SnapshotMissing", "Expired"].includes(phase);
  const danger = ["Failed", "Broken", "Invalid", "RepoUnavailable", "Cancelled"].includes(phase);
  const running = !success && !warning && !danger && phase !== "Unknown";
  const className = success ? "success" : warning ? "warning" : danger ? "danger" : running ? "running" : "";
  return `<span class="status-chip ${className}">${escapeHTML(phase || "Unknown")}</span>`;
}

function selectionCell(selection) {
  const mode = path(selection, "mode", "Namespace");
  const namespaces = path(selection, "includeNamespaces", []);
  return `${escapeHTML(mode === "Cluster" ? "整集群" : formatList(namespaces))}<span class="object-sub">${path(selection, "pvc.enabled", false) ? "包含 PVC 快照" : "仅资源元数据"}</span>`;
}

function latestPolicyRunCell(policy) {
  const relation = state.policyRelations[path(policy, "metadata.name", "")] || {};
  const task = relation.tasks?.[0];
  if (!task) return '<span class="muted-text">尚未执行</span>';
  const record = relation.recordByTask?.[path(task, "metadata.name", "")];
  const taskNode = `<span class="compact-relation-node"><em>执行</em>${statusChip(path(task, "status.phase", "Pending"))}<small>${escapeHTML(path(task, "metadata.name", "—"))}</small></span>`;
  const recordNode = record
    ? `<span class="compact-relation-node"><em>↳ 恢复点</em>${statusChip(path(record, "status.phase", "Pending"))}<small>${escapeHTML(path(record, "metadata.name", "—"))}</small></span>`
    : `<span class="compact-relation-node missing"><em>↳ 恢复点</em><strong>${terminalPhases.has(path(task, "status.phase", "")) ? "未生成" : "等待生成"}</strong><small>${formatDate(path(task, "metadata.creationTimestamp", null))}</small></span>`;
  return `<span class="compact-relation">${taskNode}${recordNode}</span>`;
}

function policyScheduleCell(policy) {
  return `<code>${escapeHTML(path(policy, "spec.schedule.cron", "—"))}</code><span class="object-sub">下次 ${formatDate(path(policy, "status.nextScheduleTime", null))}</span>`;
}

function taskRecordCell(task) {
  const recordName = path(task, "status.recordRef.name", "");
  if (recordName) return `<span class="yes-text">已生成</span><span class="object-sub">${escapeHTML(recordName)}</span>`;
  const policyName = path(task, "spec.source.policyRef.name", "");
  const relation = state.policyRelations[policyName] || {};
  const record = relation.recordByTask?.[path(task, "metadata.name", "")];
  if (record) return `${statusChip(path(record, "status.phase", "Pending"))}<span class="object-sub">${escapeHTML(path(record, "metadata.name", "—"))}</span>`;
  const phase = path(task, "status.phase", "");
  return terminalPhases.has(phase) ? "未生成" : "等待生成";
}

function progressCell(value) { return `<div style="min-width:92px"><div class="progress-track"><i style="width:${clamp(value, 0, 100)}%"></i></div><span class="progress-label">${value || 0}%</span></div>`; }

function restoreProgress(object) {
  const total = path(object, "status.progress.total", 0);
  const processed = path(object, "status.progress.processed", 0);
  return progressCell(total ? Math.round(processed * 100 / total) : 0);
}

function phaseCount(summary, phases) { return phases.reduce((total, phase) => total + (summary?.phases?.[phase] || 0), 0); }

function runningCount(summaries) {
  const terminal = ["Completed", "PartiallyFailed", "Failed", "Cancelled"];
  return [summaries["backup-tasks"], summaries["restore-tasks"]].reduce((count, summary) => {
    if (!summary) return count;
    return count + Object.entries(summary.phases || {}).filter(([phase]) => !terminal.includes(phase)).reduce((sum, [, value]) => sum + value, 0);
  }, 0);
}

function formatList(items) {
  if (!Array.isArray(items) || !items.length) return "全部";
  const text = items.length > 2 ? `${items.slice(0, 2).join(", ")} +${items.length - 2}` : items.join(", ");
  return escapeHTML(text);
}

function formatBytes(bytes) {
  const value = Number(bytes) || 0;
  if (!value) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / Math.pow(1024, index)).toFixed(index ? 1 : 0)} ${units[index]}`;
}

function formatDate(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "—";
  return date.toLocaleString("zh-CN", {month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false});
}

function compactTimestamp() {
  const date = new Date();
  return `${date.getFullYear()}${String(date.getMonth() + 1).padStart(2, "0")}${String(date.getDate()).padStart(2, "0")}-${String(date.getHours()).padStart(2, "0")}${String(date.getMinutes()).padStart(2, "0")}${String(date.getSeconds()).padStart(2, "0")}`;
}

function toast(title, message, isError = false) {
  const element = document.createElement("div");
  element.className = `toast${isError ? " error" : ""}`;
  element.innerHTML = `<span class="toast-icon">${isError ? "!" : "✓"}</span><div><strong>${escapeHTML(title)}</strong><span>${escapeHTML(message)}</span></div>`;
  document.getElementById("toast-container").appendChild(element);
  window.setTimeout(() => element.remove(), 4200);
}

function clamp(value, minimum, maximum) { return Math.min(Math.max(Number(value) || 0, minimum), maximum); }

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>'"]/g, char => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;"})[char]);
}

function escapeAttribute(value) { return escapeHTML(value); }
