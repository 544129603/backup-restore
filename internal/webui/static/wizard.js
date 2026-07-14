const wizardState = {
  step: 1,
  operation: null,
  resource: null,
  object: null,
  draft: null,
  context: null,
  criteria: {q: "", phase: "", type: ""},
  results: [],
  busy: false,
  error: "",
};

const wizardOperations = [
  {id: "query", icon: "⌕", title: "查询对象", description: "按名称、状态和类型组合查询，查看对象详情并继续修改。"},
  {id: "create", icon: "＋", title: "创建对象", description: "通过结构化表单生成 CR，提交前预览完整配置。"},
  {id: "edit", icon: "✎", title: "修改对象", description: "选择已有对象，仅编辑 CRD 允许热更新的配置字段。"},
];

const wizardResources = [
  {key: "repositories", icon: "▣", title: "备份仓库", description: "Local 或 SFTP 存储位置", create: true, edit: true},
  {key: "scopes", icon: "◎", title: "备份范围", description: "Namespace、资源与 PVC 选择", create: true, edit: true},
  {key: "policies", icon: "◷", title: "备份策略", description: "Cron、保留与并发规则", create: true, edit: true},
  {key: "backup-tasks", icon: "▶", title: "备份任务", description: "手动或定时执行记录", create: true, edit: false},
  {key: "records", icon: "◇", title: "备份记录", description: "可恢复副本与完整性状态", create: false, edit: false},
  {key: "restore-tasks", icon: "↶", title: "恢复任务", description: "恢复计划、冲突与执行状态", create: true, edit: false},
  {key: "configs", icon: "⚙", title: "全局配置", description: "并发、安全与垃圾回收", create: true, edit: true},
];

const wizardSchemas = {
  repositories: [
    {title: "基本信息", icon: "1", fields: [
      field("metadata.name", "对象名称", "text", {required: true, createOnly: true, help: "符合 Kubernetes DNS 子域名规范"}),
      field("spec.type", "仓库类型", "select", {required: true, immutable: true, refresh: true, options: [["Local", "Local 本地存储"], ["SFTP", "SFTP 远端存储"]]}),
      field("spec.enabled", "启用仓库", "checkbox"),
      field("spec.deletionProtection", "删除保护", "checkbox"),
      field("spec.minimumFreeBytes", "最小可用容量", "text", {help: "例如 1Gi、100Mi"}),
      field("spec.healthCheckInterval", "健康检查周期", "text", {help: "例如 30m"}),
    ]},
    {title: "Local 存储配置", icon: "2", condition: ["spec.type", "Local"], fields: [
      field("spec.local.mode", "本地模式", "select", {required: true, refresh: true, options: [["HostPath", "HostPath"], ["PVC", "已挂载 PVC"]]}),
      field("spec.local.path", "容器内仓库路径", "text", {required: true, condition: ["spec.local.mode", "HostPath"], help: "Docker Desktop 默认使用 /repository"}),
      field("spec.local.nodeName", "指定节点", "text", {condition: ["spec.local.mode", "HostPath"], help: "HostPath 模式必须指定节点或节点选择器"}),
      field("spec.local.pvc.namespace", "PVC Namespace", "text", {required: true, condition: ["spec.local.mode", "PVC"]}),
      field("spec.local.pvc.name", "PVC 名称", "text", {required: true, condition: ["spec.local.mode", "PVC"]}),
      field("spec.local.pvc.mountPath", "PVC 挂载路径", "text", {required: true, condition: ["spec.local.mode", "PVC"], help: "必须预先挂载到 Operator Pod"}),
      field("spec.local.uid", "目录 UID", "number"),
      field("spec.local.gid", "目录 GID", "number"),
    ]},
    {title: "SFTP 连接与凭据", icon: "2", condition: ["spec.type", "SFTP"], fields: [
      field("spec.sftp.host", "SFTP Host", "text", {required: true}),
      field("spec.sftp.port", "端口", "number", {required: true}),
      field("spec.sftp.basePath", "远端根路径", "text", {required: true, help: "必须是安全绝对路径"}),
      field("spec.sftp.auth.type", "认证类型", "select", {required: true, refresh: true, options: [["Password", "用户名密码"], ["PrivateKey", "SSH 私钥"]]}),
      field("spec.sftp.auth.usernameRef.namespace", "用户名 Secret Namespace", "text", {required: true}),
      field("spec.sftp.auth.usernameRef.name", "用户名 Secret 名称", "text", {required: true}),
      field("spec.sftp.auth.usernameRef.key", "用户名 Secret Key", "text", {required: true}),
      field("spec.sftp.auth.passwordRef.namespace", "密码 Secret Namespace", "text", {required: true, condition: ["spec.sftp.auth.type", "Password"]}),
      field("spec.sftp.auth.passwordRef.name", "密码 Secret 名称", "text", {required: true, condition: ["spec.sftp.auth.type", "Password"]}),
      field("spec.sftp.auth.passwordRef.key", "密码 Secret Key", "text", {required: true, condition: ["spec.sftp.auth.type", "Password"]}),
      field("spec.sftp.auth.privateKeyRef.namespace", "私钥 Secret Namespace", "text", {required: true, condition: ["spec.sftp.auth.type", "PrivateKey"]}),
      field("spec.sftp.auth.privateKeyRef.name", "私钥 Secret 名称", "text", {required: true, condition: ["spec.sftp.auth.type", "PrivateKey"]}),
      field("spec.sftp.auth.privateKeyRef.key", "私钥 Secret Key", "text", {required: true, condition: ["spec.sftp.auth.type", "PrivateKey"]}),
      field("spec.sftp.insecureSkipHostKeyCheck", "跳过 Host Key 校验", "checkbox", {refresh: true, help: "仅限本地测试；生产环境必须关闭"}),
      field("spec.sftp.knownHostsRef.namespace", "known_hosts Secret Namespace", "text", {required: true, condition: ["spec.sftp.insecureSkipHostKeyCheck", false]}),
      field("spec.sftp.knownHostsRef.name", "known_hosts Secret 名称", "text", {required: true, condition: ["spec.sftp.insecureSkipHostKeyCheck", false]}),
      field("spec.sftp.knownHostsRef.key", "known_hosts Secret Key", "text", {required: true, condition: ["spec.sftp.insecureSkipHostKeyCheck", false]}),
    ]},
    {title: "归档与加密", icon: "3", fields: [
      field("spec.compression.algorithm", "压缩算法", "select", {options: [["Gzip", "Gzip"], ["None", "不压缩"]]}),
      field("spec.compression.level", "压缩级别", "number", {help: "1-9"}),
      field("spec.encryption.enabled", "启用 AES-256-GCM", "checkbox", {refresh: true}),
      field("spec.encryption.keyRef.namespace", "加密 Secret Namespace", "text", {required: true, condition: ["spec.encryption.enabled", true]}),
      field("spec.encryption.keyRef.name", "加密 Secret 名称", "text", {required: true, condition: ["spec.encryption.enabled", true]}),
      field("spec.encryption.keyRef.key", "加密 Secret Key", "text", {required: true, condition: ["spec.encryption.enabled", true]}),
    ]},
  ],
  scopes: [
    {title: "基本信息", icon: "1", fields: [
      field("metadata.name", "对象名称", "text", {required: true, createOnly: true}),
      field("spec.mode", "范围模式", "select", {required: true, immutable: true, refresh: true, options: [["Namespace", "指定 Namespace"], ["Cluster", "整集群"]]}),
      field("spec.includeNamespaces", "包含 Namespace", "csv", {required: true, condition: ["spec.mode", "Namespace"], help: "多个值使用英文逗号分隔"}),
      field("spec.excludeNamespaces", "排除 Namespace", "csv", {help: "多个值使用英文逗号分隔"}),
      field("spec.labelSelector.matchLabels", "标签选择器", "map", {full: true, help: "每行一个 key=value"}),
    ]},
    {title: "资源过滤", icon: "2", fields: [
      field("spec.resources.include", "包含资源类型", "csv", {full: true, help: "例如 deployments.apps, services, configmaps"}),
      field("spec.resources.exclude", "排除资源类型", "csv", {full: true}),
      field("spec.includeClusterResources", "包含集群级资源", "checkbox"),
      field("spec.includeSecrets", "包含 Secret", "checkbox"),
      field("spec.includeCRDs", "包含 CRD", "checkbox"),
      field("spec.includeCustomResources", "包含 Custom Resource", "checkbox"),
    ]},
    {title: "PVC 快照", icon: "3", fields: [
      field("spec.pvc.enabled", "启用 CSI 快照", "checkbox", {refresh: true}),
      field("spec.pvc.snapshotClassName", "VolumeSnapshotClass", "text", {condition: ["spec.pvc.enabled", true]}),
      field("spec.pvc.include", "包含 PVC", "csv", {condition: ["spec.pvc.enabled", true]}),
      field("spec.pvc.exclude", "排除 PVC", "csv", {condition: ["spec.pvc.enabled", true]}),
      field("spec.pvc.snapshotTimeout", "快照超时", "text", {condition: ["spec.pvc.enabled", true]}),
      field("spec.pvc.failurePolicy", "失败策略", "select", {condition: ["spec.pvc.enabled", true], options: [["ContinueAndMarkPartial", "继续并标记部分失败"], ["FailFast", "快速失败"]]}),
    ]},
  ],
  policies: [
    {title: "策略引用", icon: "1", fields: [
      field("metadata.name", "对象名称", "text", {required: true, createOnly: true}),
      field("spec.scopeRef.name", "备份范围", "select", {required: true, source: "scopes"}),
      field("spec.repositoryRef.name", "备份仓库", "select", {required: true, source: "repositories"}),
      field("spec.enabled", "启用策略", "checkbox"),
      field("spec.suspend", "暂停调度", "checkbox"),
    ]},
    {title: "调度规则", icon: "2", fields: [
      field("spec.schedule.cron", "Cron 表达式", "text", {required: true, help: "标准五字段 Cron，例如 0 2 * * *"}),
      field("spec.schedule.timezone", "时区", "select", {required: true, options: [["Asia/Shanghai", "Asia/Shanghai"], ["Etc/UTC", "Etc/UTC"]]}),
      field("spec.concurrencyPolicy", "并发策略", "select", {options: [["Forbid", "禁止并发"], ["Allow", "允许并发"], ["Replace", "替换旧任务"]]}),
      field("spec.missedRunPolicy", "错过执行", "select", {options: [["RunOnce", "补偿一次"], ["Skip", "跳过"], ["RunAll", "全部补偿"]]}),
      field("spec.startingDeadline", "补偿截止时间", "text"),
      field("spec.timeout", "单次备份超时", "text"),
    ]},
    {title: "保留与重试", icon: "3", fields: [
      field("spec.retention.maxCopies", "最大副本数", "number", {required: true}),
      field("spec.retention.minCopies", "最小副本数", "number"),
      field("spec.retention.maxAgeDays", "最长保留天数", "number", {required: true}),
      field("spec.retention.deleteSnapshots", "过期时删除快照", "checkbox"),
      field("spec.retryPolicy.maxAttempts", "最大重试次数", "number"),
      field("spec.retryPolicy.backoff", "重试间隔", "text"),
    ]},
  ],
  "backup-tasks": [
    {title: "手动备份", icon: "1", fields: [
      field("metadata.name", "任务名称", "text", {required: true, createOnly: true}),
      field("spec.scopeRef.name", "备份范围", "select", {required: true, source: "scopes"}),
      field("spec.repositoryRef.name", "备份仓库", "select", {required: true, source: "repositories"}),
      field("spec.timeout", "任务超时", "text"),
      field("spec.failurePolicy", "失败策略", "select", {options: [["Continue", "继续并记录失败"], ["FailFast", "快速失败"]]}),
      field("spec.allowPartialRecord", "允许部分可用副本", "checkbox"),
    ]},
  ],
  "restore-tasks": [
    {title: "恢复来源", icon: "1", fields: [
      field("metadata.name", "任务名称", "text", {required: true, createOnly: true}),
      field("spec.backupRecordRef.name", "备份记录", "select", {required: true, source: "records"}),
      field("spec.targetClusterRef", "目标集群", "text", {required: true, immutable: true}),
      field("spec.mode", "Namespace 模式", "select", {refresh: true, options: [["Original", "恢复到原 Namespace"], ["NewNamespace", "恢复到新 Namespace"], ["Mapping", "Namespace 映射"]]}),
      field("spec.namespaceMapping", "Namespace 映射", "map", {full: true, condition: ["spec.mode", "Mapping"], help: "每行一个 source=target"}),
    ]},
    {title: "恢复内容", icon: "2", fields: [
      field("spec.resourceSelection.include", "包含资源类型", "csv", {full: true}),
      field("spec.resourceSelection.exclude", "排除资源类型", "csv", {full: true}),
      field("spec.resourceSelection.includeClusterResources", "恢复集群级资源", "checkbox"),
      field("spec.restorePVC", "恢复 PVC", "checkbox"),
      field("spec.metadataOnly", "仅恢复元数据", "checkbox"),
      field("spec.storageClassMapping", "StorageClass 映射", "map", {full: true, help: "每行一个 source=target"}),
    ]},
    {title: "安全与冲突", icon: "3", fields: [
      field("spec.dryRun", "仅执行 DryRun", "checkbox", {help: "建议先完成 DryRun 再创建实际恢复任务"}),
      field("spec.conflictPolicy.default", "默认冲突策略", "select", {options: [["Skip", "跳过"], ["Overwrite", "覆盖"], ["Rename", "重命名"], ["Fail", "遇到冲突失败"]]}),
      field("spec.conflictPolicy.allowRecreate", "允许删除后重建", "checkbox", {refresh: true}),
      field("spec.conflictPolicy.highRiskConfirmed", "确认高风险重建", "checkbox", {condition: ["spec.conflictPolicy.allowRecreate", true]}),
      field("spec.failurePolicy", "失败策略", "select", {options: [["Continue", "继续"], ["FailFast", "快速失败"]]}),
      field("spec.timeout", "恢复超时", "text"),
    ]},
  ],
  configs: [
    {title: "全局默认值", icon: "1", fields: [
      field("metadata.name", "对象名称", "text", {required: true, createOnly: true, immutable: true}),
      field("spec.defaultTimezone", "默认时区", "select", {required: true, options: [["Asia/Shanghai", "Asia/Shanghai"], ["Etc/UTC", "Etc/UTC"]]}),
      field("spec.defaultBackupTimeout", "备份超时", "text"),
      field("spec.defaultRestoreTimeout", "恢复超时", "text"),
      field("spec.defaultSnapshotTimeout", "快照超时", "text"),
      field("spec.logLevel", "日志级别", "select", {options: [["info", "info"], ["debug", "debug"], ["warn", "warn"]]}),
    ]},
    {title: "并发和 API Server", icon: "2", fields: [
      field("spec.concurrency.maxBackupTasks", "最大备份并发", "number", {required: true}),
      field("spec.concurrency.maxRestoreTasks", "最大恢复并发", "number", {required: true}),
      field("spec.concurrency.maxSnapshotsPerTask", "单任务最大快照数", "number"),
      field("spec.concurrency.maxRepositoryOperations", "最大仓库操作数", "number"),
      field("spec.kubernetesClient.qps", "Kubernetes QPS", "number", {required: true}),
      field("spec.kubernetesClient.burst", "Kubernetes Burst", "number", {required: true}),
      field("spec.kubernetesClient.pageSize", "分页大小", "number"),
    ]},
    {title: "安全和垃圾回收", icon: "3", fields: [
      field("spec.security.requireEncryptionForSecrets", "Secret 备份必须加密", "checkbox"),
      field("spec.security.allowInsecureSFTP", "允许不安全 SFTP", "checkbox"),
      field("spec.security.hookExecutionEnabled", "允许执行 Hook", "checkbox"),
      field("spec.garbageCollection.interval", "GC 周期", "text"),
      field("spec.garbageCollection.stagingGracePeriod", "临时目录宽限期", "text"),
      field("spec.garbageCollection.terminalTaskTTLDays", "终态任务保留天数", "number"),
    ]},
  ],
};

document.addEventListener("DOMContentLoaded", () => {
  document.getElementById("wizard-launch").addEventListener("click", () => openObjectWizard());
  document.getElementById("wizard-close").addEventListener("click", closeWizard);
  document.getElementById("wizard-back").addEventListener("click", wizardBack);
  document.getElementById("wizard-next").addEventListener("click", wizardNext);
  document.getElementById("wizard-advanced").addEventListener("click", openAdvancedEditor);
  document.getElementById("wizard-content").addEventListener("click", handleWizardClick);
  document.getElementById("wizard-content").addEventListener("input", handleWizardInput);
  document.getElementById("wizard-content").addEventListener("change", handleWizardInput);
});

async function openObjectWizard(operation = null, resource = null, object = null) {
  Object.assign(wizardState, {
    step: operation ? (resource ? 3 : 2) : 1,
    operation,
    resource,
    object,
    draft: object ? cleanObject(object) : null,
    context: null,
    criteria: {q: "", phase: "", type: ""},
    results: [],
    busy: false,
    error: "",
  });
  document.getElementById("wizard-dialog").showModal();
  if (resource) await prepareWizardContext();
  renderWizard();
}

function closeWizard() {
  document.getElementById("wizard-dialog").close();
}

function field(pathName, label, type, options = {}) {
  return {path: pathName, label, type, ...options};
}

function renderWizard() {
  renderWizardStepper();
  const content = document.getElementById("wizard-content");
  if (wizardState.busy) {
    content.innerHTML = '<div class="wizard-empty"><span class="spinner"></span><strong>正在准备向导</strong><span>正在读取当前集群对象和引用关系…</span></div>';
  } else if (wizardState.step === 1) {
    content.innerHTML = renderOperationStep();
  } else if (wizardState.step === 2) {
    content.innerHTML = renderResourceStep();
  } else if (wizardState.step === 3) {
    content.innerHTML = wizardState.operation === "query" ? renderQueryConditions() : renderObjectForm();
  } else {
    content.innerHTML = wizardState.operation === "query" ? renderQueryResults() : renderObjectPreview();
  }
  renderWizardFooter();
}

function renderWizardStepper() {
  const labels = wizardState.operation === "query"
    ? ["选择操作", "选择对象", "查询条件", "查询结果"]
    : ["选择操作", "选择对象", "配置字段", "预览确认"];
  document.getElementById("wizard-stepper").innerHTML = labels.map((label, index) => {
    const step = index + 1;
    const className = step < wizardState.step ? "completed" : step === wizardState.step ? "active" : "";
    return `<div class="wizard-step ${className}"><span class="wizard-step-index">${step < wizardState.step ? "✓" : step}</span><span>${label}</span></div>`;
  }).join("");
}

function renderOperationStep() {
  return `<div class="wizard-intro"><h3>您希望执行什么操作？</h3><p>向导会根据操作类型只展示有效对象和可修改字段。</p></div>
    <div class="choice-grid">${wizardOperations.map(item => choiceCard("operation", item.id, item.icon, item.title, item.description, wizardState.operation === item.id)).join("")}</div>`;
}

function renderResourceStep() {
  return `<div class="wizard-intro"><h3>选择 Kubernetes 对象</h3><p>当前集群：${escapeHTML(state.clusterRef)}。不可用于当前操作的对象已禁用。</p></div>
    <div class="choice-grid resource-choices">${wizardResources.map(item => {
      const disabled = wizardState.operation === "create" ? !item.create : wizardState.operation === "edit" ? !item.edit : false;
      const tag = disabled ? (wizardState.operation === "edit" ? "规格不可变" : "由系统生成") : "";
      return choiceCard("resource", item.key, item.icon, item.title, item.description, wizardState.resource === item.key, disabled, tag);
    }).join("")}</div>`;
}

function choiceCard(type, value, icon, title, description, selected, disabled = false, tag = "") {
  return `<button class="choice-card ${selected ? "selected" : ""} ${disabled ? "disabled" : ""}" data-${type}="${escapeAttribute(value)}" ${disabled ? "disabled" : ""}>
    ${tag ? `<span class="choice-tag">${escapeHTML(tag)}</span>` : ""}<span class="choice-icon">${icon}</span><strong>${escapeHTML(title)}</strong><small>${escapeHTML(description)}</small></button>`;
}

function renderQueryConditions() {
  const resource = resourceMeta(wizardState.resource);
  return `<div class="wizard-intro"><h3>设置${resource.title}查询条件</h3><p>条件为空时查询当前集群下全部对象，多个条件之间为 AND 关系。</p></div>
    <div class="query-panel"><div class="form-grid">
      ${renderSimpleField("q", "名称或内容关键字", "text", wizardState.criteria.q, "支持对象名称、引用、状态消息和 spec 内容")}
      ${renderSimpleField("phase", "精确状态", "select", wizardState.criteria.phase, "", phaseOptions())}
      ${renderSimpleField("type", "类型 / 模式 / 触发方式", "text", wizardState.criteria.type, "例如 Local、Namespace、Manual")}
    </div><div class="query-hint">查询请求在服务端按 <code>clusterRef=${escapeHTML(state.clusterRef)}</code> 隔离，并限制返回当前管理界面允许访问的 Cluster-scoped 对象。</div>
    ${wizardError()}</div>`;
}

function renderSimpleField(name, label, type, value, help = "", options = []) {
  if (type === "select") {
    return `<div class="form-field"><label>${label}</label><select data-query-field="${name}">${options.map(([optionValue, optionLabel]) => `<option value="${escapeAttribute(optionValue)}" ${optionValue === value ? "selected" : ""}>${escapeHTML(optionLabel)}</option>`).join("")}</select>${help ? `<small>${help}</small>` : ""}</div>`;
  }
  return `<div class="form-field"><label>${label}</label><input data-query-field="${name}" value="${escapeAttribute(value)}">${help ? `<small>${help}</small>` : ""}</div>`;
}

function phaseOptions() {
  return [["", "全部状态"], ...["Pending", "Ready", "Paused", "Running", "Completed", "PartiallyFailed", "Failed", "Cancelled", "Available", "Broken", "Expired"].map(value => [value, value])];
}

function renderObjectForm() {
  const meta = resourceMeta(wizardState.resource);
  if (wizardState.operation === "edit" && !wizardState.object) {
    const items = wizardState.context?.items || [];
    return `<div class="wizard-intro"><h3>选择要修改的${meta.title}</h3><p>选中对象后，向导将加载当前配置并锁定不可变字段。</p></div>
      <div class="selected-object"><div class="form-field"><label>已有对象</label><select id="wizard-object-select"><option value="">请选择对象</option>${items.map(item => `<option value="${escapeAttribute(item.metadata.name)}">${escapeHTML(item.metadata.name)} · ${escapeHTML(path(item, "status.phase", "Pending"))}</option>`).join("")}</select></div></div>${wizardError()}`;
  }
  if (!wizardState.draft) return '<div class="wizard-empty"><strong>无法生成配置表单</strong><span>请返回重新选择对象。</span></div>';
  const schema = wizardSchemas[wizardState.resource] || [];
  return `<div class="wizard-intro"><h3>${wizardState.operation === "create" ? "配置新" : "修改"}${meta.title}</h3><p>带红色星号的字段必填；灰色字段在对象创建后不可修改。</p></div>
    ${schema.filter(section => conditionMatches(section.condition)).map(section => `<section class="wizard-section"><h4 class="wizard-section-title"><span>${section.icon}</span>${section.title}</h4><div class="form-grid">${section.fields.filter(item => conditionMatches(item.condition)).map(renderSchemaField).join("")}</div></section>`).join("")}${wizardError()}`;
}

function renderSchemaField(item) {
  const value = objectPath(wizardState.draft, item.path);
  const disabled = wizardState.operation === "edit" && (item.immutable || item.createOnly || item.path === "metadata.name");
  const required = item.required ? "<em>*</em>" : "";
  const fieldClass = item.full ? "form-field full" : "form-field";
  let control = "";
  if (item.type === "checkbox") {
    control = `<label class="form-check"><input type="checkbox" data-wizard-path="${item.path}" data-field-type="checkbox" ${value ? "checked" : ""} ${disabled ? "disabled" : ""} ${item.refresh ? 'data-refresh="true"' : ""}><span>${item.label}</span></label>`;
    return `<div class="${fieldClass}">${control}${item.help ? `<small>${item.help}</small>` : ""}</div>`;
  }
  if (item.type === "select") {
    const options = resolveOptions(item);
    control = `<select data-wizard-path="${item.path}" data-field-type="select" ${disabled ? "disabled" : ""} ${item.refresh ? 'data-refresh="true"' : ""}>${options.map(([optionValue, optionLabel]) => `<option value="${escapeAttribute(optionValue)}" ${String(optionValue) === String(value ?? "") ? "selected" : ""}>${escapeHTML(optionLabel)}</option>`).join("")}</select>`;
  } else if (["csv", "map"].includes(item.type)) {
    const displayValue = item.type === "csv" ? arrayToText(value) : mapToText(value);
    control = `<textarea data-wizard-path="${item.path}" data-field-type="${item.type}" ${disabled ? "disabled" : ""}>${escapeHTML(displayValue)}</textarea>`;
  } else {
    control = `<input type="${item.type === "number" ? "number" : "text"}" data-wizard-path="${item.path}" data-field-type="${item.type}" value="${escapeAttribute(value ?? "")}" ${disabled ? "disabled" : ""}>`;
  }
  return `<div class="${fieldClass}"><label>${item.label}${required}</label>${control}${item.help ? `<small>${item.help}</small>` : ""}</div>`;
}

function resolveOptions(item) {
  if (item.source) {
    const items = wizardState.context?.[item.source] || [];
    return [["", "请选择"], ...items.map(object => [object.metadata.name, `${object.metadata.name} · ${path(object, "status.phase", "Pending")}`])];
  }
  return item.options || [];
}

function renderObjectPreview() {
  const meta = resourceMeta(wizardState.resource);
  const object = normalizeDraft(structuredClone(wizardState.draft));
  const name = path(object, "metadata.name", "—");
  const warning = wizardState.resource === "restore-tasks" && !path(object, "spec.dryRun", true)
    ? "这是实际恢复任务，可能覆盖或创建业务资源。提交前请确认目标 Namespace、PVC 与冲突策略。"
    : wizardState.operation === "edit" ? "更新会立即触发 Operator Reconcile；已有运行任务不会随引用对象修改而变化。" : "提交后对象将由 Admission Webhook 再次校验。";
  return `<div class="wizard-intro"><h3>确认${wizardState.operation === "create" ? "创建" : "修改"}${meta.title}</h3><p>请核对摘要与完整配置，确认无误后提交 Kubernetes API。</p></div>
    <div class="preview-layout"><div class="preview-summary">
      <div class="preview-card"><span>操作</span><strong>${wizardState.operation === "create" ? "创建" : "修改"}</strong></div>
      <div class="preview-card"><span>对象类型</span><strong>${meta.title}</strong></div>
      <div class="preview-card"><span>对象名称</span><strong>${escapeHTML(name)}</strong></div>
      <div class="preview-card"><span>目标集群</span><strong>${escapeHTML(path(object, "spec.clusterRef", state.clusterRef))}</strong></div>
      <div class="preview-warning">${escapeHTML(warning)}</div>
    </div><pre class="preview-json">${escapeHTML(JSON.stringify(object, null, 2))}</pre></div>${wizardError()}`;
}

function renderQueryResults() {
  const meta = resourceMeta(wizardState.resource);
  if (!wizardState.results.length) {
    return `<div class="wizard-intro"><h3>${meta.title}查询结果</h3><p>没有对象符合当前组合条件。</p></div><div class="wizard-empty"><span class="choice-icon">⌕</span><strong>未找到匹配对象</strong><span>返回上一步调整关键字或状态条件。</span></div>${wizardError()}`;
  }
  return `<div class="wizard-intro"><h3>${meta.title}查询结果</h3><p>共找到 ${wizardState.results.length} 个对象，可查看详情${meta.edit ? "或进入修改向导" : ""}。</p></div>
    <div class="query-results">${wizardState.results.map((object, index) => `<div class="query-result"><div class="query-result-name"><strong>${escapeHTML(object.metadata.name)}</strong><span>${escapeHTML(path(object, "status.message", path(object, "status.reason", "无状态消息")))}</span></div>${statusChip(path(object, "status.phase", "Pending"))}<div class="query-result-actions"><button class="action-button" data-result-action="view" data-result-index="${index}">查看详情</button>${meta.edit ? `<button class="action-button" data-result-action="edit" data-result-index="${index}">修改</button>` : ""}</div></div>`).join("")}</div>${wizardError()}`;
}

function renderWizardFooter() {
  const back = document.getElementById("wizard-back");
  const next = document.getElementById("wizard-next");
  const advanced = document.getElementById("wizard-advanced");
  back.disabled = wizardState.step === 1 || wizardState.busy;
  advanced.classList.toggle("hidden", !(wizardState.step === 3 && ["create", "edit"].includes(wizardState.operation) && wizardState.draft));
  next.disabled = wizardState.busy;
  if (wizardState.step < 4) next.textContent = "下一步";
  else if (wizardState.operation === "query") next.textContent = "完成";
  else next.textContent = wizardState.operation === "create" ? "确认创建" : "保存修改";
  document.getElementById("wizard-title").textContent = wizardState.resource ? `${resourceMeta(wizardState.resource).title} · 操作向导` : "操作向导";
}

async function handleWizardClick(event) {
  const operation = event.target.closest("[data-operation]");
  if (operation) {
    wizardState.operation = operation.dataset.operation;
    wizardState.resource = null;
    renderWizard();
    return;
  }
  const resource = event.target.closest("[data-resource]");
  if (resource && !resource.disabled) {
    wizardState.resource = resource.dataset.resource;
    renderWizard();
    return;
  }
  const resultAction = event.target.closest("[data-result-action]");
  if (resultAction) {
    const object = wizardState.results[Number(resultAction.dataset.resultIndex)];
    if (!object) return;
    if (resultAction.dataset.resultAction === "view") {
      closeWizard();
      showDetail(wizardState.resource, object);
    } else {
      await openObjectWizard("edit", wizardState.resource, object);
    }
  }
}

function handleWizardInput(event) {
  const queryField = event.target.dataset.queryField;
  if (queryField) {
    wizardState.criteria[queryField] = event.target.value;
    return;
  }
  if (event.target.id === "wizard-object-select") {
    const selected = (wizardState.context?.items || []).find(item => item.metadata.name === event.target.value);
    wizardState.object = selected || null;
    wizardState.draft = selected ? cleanObject(selected) : null;
    renderWizard();
    return;
  }
  const pathName = event.target.dataset.wizardPath;
  if (!pathName || !wizardState.draft) return;
  const fieldType = event.target.dataset.fieldType;
  let value = event.target.value;
  if (fieldType === "checkbox") value = event.target.checked;
  if (fieldType === "number") value = value === "" ? 0 : Number(value);
  if (fieldType === "csv") value = textToArray(value);
  if (fieldType === "map") value = textToMap(value);
  setObjectPath(wizardState.draft, pathName, value);
  wizardState.error = "";
  if (event.target.dataset.refresh === "true") {
    wizardState.draft = normalizeDraft(wizardState.draft);
    renderWizard();
  }
}

async function wizardNext() {
  wizardState.error = "";
  if (wizardState.step === 1) {
    if (!wizardState.operation) return wizardFail("请选择查询、创建或修改操作。", 1);
    wizardState.step = 2;
    renderWizard();
    return;
  }
  if (wizardState.step === 2) {
    if (!wizardState.resource) return wizardFail("请选择要管理的对象类型。", 2);
    wizardState.step = 3;
    await prepareWizardContext();
    renderWizard();
    return;
  }
  if (wizardState.step === 3) {
    if (wizardState.operation === "edit" && !wizardState.draft) return wizardFail("请选择要修改的对象。", 3);
    if (wizardState.operation === "query") {
      await executeWizardQuery();
      return;
    }
    wizardState.draft = normalizeDraft(wizardState.draft);
    const errors = validateWizardDraft(wizardState.draft);
    if (errors.length) return wizardFail(errors.join("；"), 3);
    wizardState.step = 4;
    renderWizard();
    return;
  }
  if (wizardState.operation === "query") {
    closeWizard();
    return;
  }
  await submitWizardObject();
}

function wizardBack() {
  if (wizardState.step <= 1) return;
  wizardState.step--;
  wizardState.error = "";
  if (wizardState.step === 2) {
    wizardState.draft = null;
    wizardState.object = null;
    wizardState.context = null;
  }
  renderWizard();
}

async function prepareWizardContext() {
  wizardState.busy = true;
  renderWizard();
  try {
    const keys = ["repositories", "scopes", "records", wizardState.resource];
    const uniqueKeys = [...new Set(keys)];
    const responses = await Promise.all(uniqueKeys.map(key => api(`${API_BASE}/${key}`)));
    const context = {};
    uniqueKeys.forEach((key, index) => { context[key] = responses[index].items || []; });
    context.items = context[wizardState.resource] || [];
    wizardState.context = context;
    if (wizardState.operation === "create" && !wizardState.draft) {
      const references = {
        repository: context.repositories?.[0]?.metadata?.name,
        scope: context.scopes?.[0]?.metadata?.name,
        record: context.records?.[0]?.metadata?.name,
      };
      wizardState.draft = resourceDefinitions[wizardState.resource].template(references);
      initializeDraftDefaults(wizardState.draft);
    }
  } catch (error) {
    wizardState.error = error.message;
  } finally {
    wizardState.busy = false;
  }
}

function initializeDraftDefaults(draft) {
  if (wizardState.resource === "repositories" && path(draft, "spec.type") === "Local") {
    setObjectPath(draft, "spec.local.uid", 65532);
    setObjectPath(draft, "spec.local.gid", 65532);
  }
  if (wizardState.resource === "restore-tasks") {
    setObjectPath(draft, "spec.dryRun", true);
    setObjectPath(draft, "spec.restorePVC", false);
  }
}

async function executeWizardQuery() {
  wizardState.busy = true;
  renderWizard();
  try {
    const query = new URLSearchParams({limit: "500"});
    Object.entries(wizardState.criteria).forEach(([key, value]) => { if (value) query.set(key, value); });
    const response = await api(`${API_BASE}/${wizardState.resource}?${query}`);
    wizardState.results = (response.items || []).sort((left, right) => new Date(right.metadata.creationTimestamp) - new Date(left.metadata.creationTimestamp));
    wizardState.step = 4;
  } catch (error) {
    wizardState.error = error.message;
  } finally {
    wizardState.busy = false;
    renderWizard();
  }
}

async function submitWizardObject() {
  wizardState.busy = true;
  renderWizard();
  try {
    const object = normalizeDraft(structuredClone(wizardState.draft));
    const name = object.metadata.name;
    const endpoint = wizardState.operation === "edit" ? `${API_BASE}/${wizardState.resource}/${encodeURIComponent(name)}` : `${API_BASE}/${wizardState.resource}`;
    await api(endpoint, {method: wizardState.operation === "edit" ? "PUT" : "POST", body: JSON.stringify(object)});
    closeWizard();
    toast(wizardState.operation === "edit" ? "修改成功" : "创建成功", `${resourceMeta(wizardState.resource).title} ${name} 已提交`);
    navigate(wizardState.resource);
  } catch (error) {
    wizardState.error = error.message;
    wizardState.busy = false;
    renderWizard();
  }
}

function openAdvancedEditor() {
  if (!wizardState.draft) return;
  const resource = wizardState.resource;
  const object = wizardState.operation === "edit" ? wizardState.object : null;
  const prepared = normalizeDraft(structuredClone(wizardState.draft));
  closeWizard();
  openEditor(resource, object, prepared);
}

function validateWizardDraft(draft) {
  const errors = [];
  const name = path(draft, "metadata.name", "");
  if (!name) errors.push("对象名称不能为空");
  else if (name.length > 253 || !/^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$/.test(name)) errors.push("对象名称不符合 DNS 子域名规范");
  const schema = wizardSchemas[wizardState.resource] || [];
  schema.filter(section => conditionMatches(section.condition, draft)).forEach(section => {
    section.fields.filter(item => item.required && conditionMatches(item.condition, draft)).forEach(item => {
      const value = objectPath(draft, item.path);
      if (value === undefined || value === null || value === "" || (Array.isArray(value) && !value.length)) errors.push(`${item.label}不能为空`);
    });
  });
  if (wizardState.resource === "policies" && String(path(draft, "spec.schedule.cron", "")).trim().split(/\s+/).length !== 5) errors.push("Cron 必须包含五个字段");
  if (wizardState.resource === "scopes" && path(draft, "spec.mode") === "Namespace" && !path(draft, "spec.includeNamespaces", []).length) errors.push("Namespace 模式至少包含一个 Namespace");
  if (wizardState.resource === "restore-tasks") {
    if (path(draft, "spec.restorePVC", false) && path(draft, "spec.metadataOnly", false)) errors.push("恢复 PVC 与仅恢复元数据不能同时启用");
    if (path(draft, "spec.conflictPolicy.allowRecreate", false) && !path(draft, "spec.conflictPolicy.highRiskConfirmed", false)) errors.push("允许删除重建时必须确认高风险操作");
  }
  return [...new Set(errors)];
}

function normalizeDraft(draft) {
  if (!draft) return draft;
  if (!path(draft, "spec.clusterRef") && wizardState.resource !== "configs") setObjectPath(draft, "spec.clusterRef", state.clusterRef);
  if (wizardState.resource === "repositories") {
    const type = path(draft, "spec.type");
    if (type === "Local") {
      delete draft.spec.sftp;
      draft.spec.local ||= {mode: "HostPath", path: "/repository", nodeName: "desktop-control-plane", uid: 65532, gid: 65532};
    } else {
      delete draft.spec.local;
      draft.spec.sftp ||= {host: "", port: 22, basePath: "/backups", auth: {type: "Password", usernameRef: {namespace: "backup-system", name: "sftp-credentials", key: "username"}, passwordRef: {namespace: "backup-system", name: "sftp-credentials", key: "password"}}, insecureSkipHostKeyCheck: false};
      const auth = draft.spec.sftp.auth || (draft.spec.sftp.auth = {});
      if (auth.type === "Password") delete auth.privateKeyRef;
      else delete auth.passwordRef;
      if (draft.spec.sftp.insecureSkipHostKeyCheck) delete draft.spec.sftp.knownHostsRef;
    }
    if (!path(draft, "spec.encryption.enabled", false)) delete draft.spec.encryption.keyRef;
  }
  if (wizardState.resource === "scopes") {
    if (path(draft, "spec.mode") === "Cluster") delete draft.spec.includeNamespaces;
    if (!Object.keys(path(draft, "spec.labelSelector.matchLabels", {})).length) delete draft.spec.labelSelector;
  }
  return draft;
}

function conditionMatches(condition, target = wizardState.draft) {
  if (!condition) return true;
  return objectPath(target, condition[0]) === condition[1];
}

function objectPath(object, dottedPath) {
  return dottedPath.split(".").reduce((value, key) => value == null ? undefined : value[key], object);
}

function setObjectPath(object, dottedPath, value) {
  const parts = dottedPath.split(".");
  let current = object;
  parts.forEach((part, index) => {
    if (index === parts.length - 1) current[part] = value;
    else current = current[part] ||= {};
  });
}

function arrayToText(value) { return Array.isArray(value) ? value.join(", ") : ""; }
function textToArray(value) { return value.split(/[,\n]/).map(item => item.trim()).filter(Boolean); }
function mapToText(value) { return value && typeof value === "object" ? Object.entries(value).map(([key, item]) => `${key}=${item}`).join("\n") : ""; }
function textToMap(value) {
  const result = {};
  value.split("\n").map(line => line.trim()).filter(Boolean).forEach(line => {
    const separator = line.indexOf("=") >= 0 ? line.indexOf("=") : line.indexOf(":");
    if (separator > 0) result[line.slice(0, separator).trim()] = line.slice(separator + 1).trim();
  });
  return result;
}

function resourceMeta(resource) {
  const item = wizardResources.find(candidate => candidate.key === resource);
  return item || {key: resource, title: resource, create: false, edit: false};
}

function wizardFail(message, step = wizardState.step) {
  wizardState.error = message;
  wizardState.step = step;
  renderWizard();
}

function wizardError() {
  return wizardState.error ? `<div class="wizard-error">${escapeHTML(wizardState.error)}</div>` : "";
}
