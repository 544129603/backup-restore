const API_BASE = "/api/resources";

const state = {
  currentView: "overview",
  items: [],
  filteredItems: [],
  clusterRef: "docker-desktop",
  editor: { mode: "create", resource: null, name: null },
  detail: null,
  deletion: null,
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
  scopes: {
    title: "备份范围", singular: "备份范围", kind: "BackupScope", creatable: true, editable: true,
    columns: [
      ["模式", o => path(o, "spec.mode", "—")],
      ["命名空间", o => formatList(path(o, "spec.includeNamespaces", []))],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["资源对象", o => String(path(o, "status.preview.resourceObjectCount", 0))],
      ["PVC", o => `${path(o, "status.preview.pvcCount", 0)} / ${path(o, "spec.pvc.enabled", false) ? "快照" : "不快照"}`],
    ],
    actions: () => [action("refresh", "刷新预览"), action("edit", "编辑"), action("delete", "删除", "danger")],
    template: () => baseObject("BackupScope", "namespace-scope", {
      clusterRef: state.clusterRef, mode: "Namespace", includeNamespaces: ["default"], excludeNamespaces: [],
      resources: {include: ["configmaps", "secrets", "services", "deployments.apps"], exclude: ["events", "pods"]},
      includeClusterResources: false, includeSecrets: true, includeCRDs: false, includeCustomResources: true,
      pvc: {enabled: false, snapshotTimeout: "10m", failurePolicy: "ContinueAndMarkPartial", lifecycle: "RetainAfterRecordDeletion"},
      consistencyMode: "CrashConsistent",
    }),
  },
  policies: {
    title: "备份策略", singular: "备份策略", kind: "BackupPolicy", creatable: true, editable: true,
    columns: [
      ["Cron", o => `<code>${escapeHTML(path(o, "spec.schedule.cron", "—"))}</code>`],
      ["范围 / 仓库", o => `${escapeHTML(path(o, "spec.scopeRef.name", "—"))}<span class="object-sub">${escapeHTML(path(o, "spec.repositoryRef.name", "—"))}</span>`],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["并发", o => path(o, "spec.concurrencyPolicy", "Forbid")],
      ["下次执行", o => formatDate(path(o, "status.nextScheduleTime", null))],
    ],
    actions: o => [action("run", "立即执行"), action(path(o, "spec.suspend", false) ? "resume" : "suspend", path(o, "spec.suspend", false) ? "启用" : "停用"), action("edit", "编辑"), action("delete", "删除", "danger")],
    template: refs => baseObject("BackupPolicy", "daily-backup", {
      clusterRef: state.clusterRef,
      scopeRef: {name: refs.scope || "namespace-scope"}, repositoryRef: {name: refs.repository || "local-repository"},
      schedule: {cron: "0 2 * * *", timezone: "Asia/Shanghai"}, enabled: true, suspend: false,
      concurrencyPolicy: "Forbid", missedRunPolicy: "RunOnce", startingDeadline: "1h", maxCatchUpRuns: 1,
      retention: {maxCopies: 7, minCopies: 1, maxAgeDays: 30, deleteSnapshots: false},
      retryPolicy: {maxAttempts: 3, backoff: "30s", maxBackoff: "10m"}, timeout: "4h",
    }),
  },
  "backup-tasks": {
    title: "备份任务", singular: "备份任务", kind: "BackupTask", creatable: true, editable: false,
    columns: [
      ["触发方式", o => path(o, "spec.trigger", "Manual")],
      ["范围 / 仓库", o => `${escapeHTML(path(o, "spec.scopeRef.name", "—"))}<span class="object-sub">${escapeHTML(path(o, "spec.repositoryRef.name", "—"))}</span>`],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
      ["进度", o => progressCell(path(o, "status.progress.percent", 0))],
      ["备份大小", o => formatBytes(path(o, "status.backupBytes", 0))],
      ["开始时间", o => formatDate(path(o, "status.startedAt", path(o, "metadata.creationTimestamp", null)))],
    ],
    actions: o => [action("view", "详情"), ...(!terminalPhases.has(path(o, "status.phase", "")) ? [action("cancel", "取消", "danger")] : []), action("delete", "删除", "danger")],
    template: refs => baseObject("BackupTask", `manual-${compactTimestamp()}`, {
      clusterRef: state.clusterRef, trigger: "Manual", scopeRef: {name: refs.scope || "namespace-scope"},
      repositoryRef: {name: refs.repository || "local-repository"}, timeout: "4h",
      retryPolicy: {maxAttempts: 3, backoff: "30s", maxBackoff: "10m"},
      failurePolicy: "Continue", allowPartialRecord: true, idempotencyKey: `webui/manual/${Date.now()}`,
    }),
  },
  records: {
    title: "备份记录", singular: "备份记录", kind: "BackupRecord", creatable: false, editable: false,
    columns: [
      ["仓库", o => path(o, "spec.repositoryRef.name", "—")],
      ["状态", o => statusChip(path(o, "status.phase", "Pending"))],
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
      ["备份记录", o => path(o, "spec.backupRecordRef.name", "—")],
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
  document.getElementById("copy-json").addEventListener("click", async () => {
    await navigator.clipboard.writeText(document.getElementById("detail-json").textContent);
    toast("已复制", "对象 JSON 已复制到剪贴板");
  });
  document.getElementById("delete-form").addEventListener("submit", submitDelete);
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
      card("可恢复副本", phaseCount(summaries.records, ["Available", "PartiallyAvailable"]), `共 ${summaries.records?.total || 0} 个记录`, "◇", "#169b62", "#eaf8f1"),
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
    ["备份范围", summaries.scopes, ["Ready"]],
    ["定时策略", summaries.policies, ["Ready", "Paused"]],
    ["备份任务", summaries["backup-tasks"], ["Completed"]],
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
  const [repositories, scopes, records] = await Promise.all([
    api(`${API_BASE}/repositories`), api(`${API_BASE}/scopes`), api(`${API_BASE}/records`),
  ]);
  const first = list => (list.items || [])[0]?.metadata?.name;
  return {repository: first(repositories), scope: first(scopes), record: first(records)};
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
  state.detail = {resource, object};
  document.getElementById("detail-kind").textContent = definition.kind;
  document.getElementById("detail-title").textContent = object.metadata?.name || "—";
  document.getElementById("detail-summary").innerHTML = [
    ["状态", path(object, "status.phase", "Pending")],
    ["集群", path(object, "spec.clusterRef", state.clusterRef)],
    ["资源版本", path(object, "metadata.resourceVersion", "—")],
    ["创建时间", formatDate(path(object, "metadata.creationTimestamp", null))],
  ].map(([label, value]) => `<div class="detail-cell"><span>${label}</span><strong>${escapeHTML(value)}</strong></div>`).join("");
  document.getElementById("detail-json").textContent = JSON.stringify(object, null, 2);
  document.getElementById("detail-edit").classList.toggle("hidden", !definition.editable);
  document.getElementById("detail-dialog").showModal();
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
