var BrainNotesApp = (() => {
  // internal/web/static_src/filters.ts
  var filterChangeEventName = "bn:filterschange";
  var sharedFilterParamKeys = [
    "tag",
    "category",
    "status",
    "type",
    "folder",
    "has",
    "missing",
    "orphan",
    "broken",
    "dead_end",
    "age",
    "created",
    "min_backlinks",
    "max_backlinks",
    "min_links",
    "max_links"
  ];
  var repeatedFilterParams = /* @__PURE__ */ new Set(["tag", "category"]);
  var normalizeParamValue = (value) => String(value || "").trim();
  var notePathForKey = (key) => {
    if (!key) {
      return "";
    }
    const segments = String(key).split("/");
    const escaped = segments.map((segment) => encodeURIComponent(segment));
    return `/n/${escaped.join("/")}`;
  };
  var readPageQuery = () => {
    const params = new URLSearchParams(window.location.search);
    return normalizeParamValue(params.get("q"));
  };
  var hasActiveSharedFilters = (params) => {
    const source = params || new URLSearchParams(window.location.search);
    return sharedFilterParamKeys.some(
      (key) => source.getAll(key).some((value) => normalizeParamValue(value) !== "")
    );
  };
  var copySharedFilterParams = (fromParams, toParams) => {
    for (const key of sharedFilterParamKeys) {
      for (const value of fromParams.getAll(key)) {
        const normalized = normalizeParamValue(value);
        if (normalized !== "") {
          toParams.append(key, normalized);
        }
      }
    }
  };
  var updateBrowserQuery = (params) => {
    if (!window.history || typeof window.history.replaceState !== "function") {
      return;
    }
    const query = params.toString();
    const nextURL = `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`;
    const currentURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (nextURL !== currentURL) {
      window.history.replaceState(null, "", nextURL);
    }
  };
  var syncPageQueryParam = (query) => {
    const params = new URLSearchParams(window.location.search);
    const normalized = normalizeParamValue(query);
    if (normalized === "") {
      params.delete("q");
    } else {
      params.set("q", normalized);
    }
    updateBrowserQuery(params);
  };
  var buildFilteredAPIURL = (path, query) => {
    const params = new URLSearchParams();
    const normalizedQuery = normalizeParamValue(query);
    if (normalizedQuery !== "") {
      params.set("q", normalizedQuery);
    }
    copySharedFilterParams(new URLSearchParams(window.location.search), params);
    const queryString = params.toString();
    return queryString ? `${path}?${queryString}` : path;
  };
  var buildSearchAPIURL = (query, includeSharedFilters) => {
    if (includeSharedFilters) {
      return buildFilteredAPIURL("/api/search", query);
    }
    const params = new URLSearchParams();
    const normalized = normalizeParamValue(query);
    if (normalized !== "") {
      params.set("q", normalized);
    }
    const queryString = params.toString();
    return queryString ? `/api/search?${queryString}` : "/api/search";
  };
  var dispatchFiltersChange = (target, detail) => {
    target.dispatchEvent(new CustomEvent(filterChangeEventName, { detail }));
  };
  var shouldHandleFilterChangeForMode = (event, mode) => !event.detail.mode || event.detail.mode === mode;

  // internal/web/static_src/api-internal.ts
  var asRecord = (value) => value && typeof value === "object" ? value : null;
  var normalizeStringArray = (value) => Array.isArray(value) ? value.map((item) => normalizeParamValue(item)).filter((item) => item !== "") : [];
  var readCount = (value) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  };
  var readBoolean = (value) => {
    if (typeof value === "boolean") {
      return value;
    }
    const normalized = normalizeParamValue(value).toLowerCase();
    return normalized === "true" || normalized === "1";
  };
  var readRecordValue = (record, keys) => {
    for (const key of keys) {
      if (Object.prototype.hasOwnProperty.call(record, key)) {
        return record[key];
      }
    }
    return void 0;
  };
  var normalizeBriefActivitySummary = (value) => {
    const record = asRecord(value) || {};
    return {
      WindowDays: readCount(readRecordValue(record, ["WindowDays", "window_days", "windowDays"])),
      VisitDays: readCount(readRecordValue(record, ["VisitDays", "visit_days", "visitDays"])),
      OpenCount: readCount(readRecordValue(record, ["OpenCount", "open_count", "openCount"]))
    };
  };
  var normalizeManagementResult = (payload) => {
    const record = asRecord(payload) || {};
    return readBoolean(record.ok) ? {
      ok: true,
      message: normalizeParamValue(record.message) || void 0,
      key: normalizeParamValue(record.key) || void 0,
      path: normalizeParamValue(record.path) || void 0,
      rel_path: normalizeParamValue(record.rel_path) || void 0,
      journal_id: normalizeParamValue(record.journal_id) || void 0
    } : {
      ok: false,
      code: normalizeParamValue(record.code) || void 0,
      message: normalizeParamValue(record.message) || void 0
    };
  };

  // internal/web/static_src/api-brief.ts
  var normalizeBriefRollingActivity = (value) => {
    const record = asRecord(value) || {};
    return {
      Last7Days: normalizeBriefActivitySummary(
        readRecordValue(record, ["Last7Days", "last_7_days", "last7Days"])
      ),
      Last14Days: normalizeBriefActivitySummary(
        readRecordValue(record, ["Last14Days", "last_14_days", "last14Days"])
      )
    };
  };
  var normalizeBriefNoteRef = (value) => {
    const record = asRecord(value) || {};
    return {
      RelPath: normalizeParamValue(readRecordValue(record, ["RelPath", "rel_path", "relPath"])),
      Title: normalizeParamValue(readRecordValue(record, ["Title", "title"]))
    };
  };
  var normalizeBriefCandidateMetadata = (value) => {
    const record = asRecord(value) || {};
    return {
      Score: Number(readRecordValue(record, ["Score", "score"])) || 0,
      Relationship: normalizeParamValue(readRecordValue(record, ["Relationship", "relationship"])),
      Bridge: normalizeParamValue(readRecordValue(record, ["Bridge", "bridge"])),
      Reasons: normalizeStringArray(readRecordValue(record, ["Reasons", "reasons"]))
    };
  };
  var normalizeBriefCard = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const id = normalizeParamValue(readRecordValue(record, ["ID", "id"]));
    if (id === "") {
      return null;
    }
    const candidate = asRecord(readRecordValue(record, ["Candidate", "candidate"]));
    return {
      ID: id,
      Slot: normalizeParamValue(readRecordValue(record, ["Slot", "slot"])),
      Source: normalizeBriefNoteRef(readRecordValue(record, ["Source", "source"])),
      Candidate: candidate ? normalizeBriefNoteRef(candidate) : void 0,
      Explanation: normalizeParamValue(readRecordValue(record, ["Explanation", "explanation"])),
      Excerpt: normalizeParamValue(readRecordValue(record, ["Excerpt", "excerpt"])),
      Metadata: normalizeBriefCandidateMetadata(readRecordValue(record, ["Metadata", "metadata"]))
    };
  };
  var normalizeBriefUnavailable = (value) => {
    const record = asRecord(value);
    if (!record) {
      return void 0;
    }
    const message = normalizeParamValue(readRecordValue(record, ["Message", "message"]));
    const slot = normalizeParamValue(readRecordValue(record, ["Slot", "slot"]));
    if (slot === "" && message === "") {
      return void 0;
    }
    return {
      Slot: slot,
      Message: message
    };
  };
  var normalizeBriefSlotResults = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const slot = normalizeParamValue(readRecordValue(record, ["Slot", "slot"]));
      const card = normalizeBriefCard(readRecordValue(record, ["Card", "card"]));
      const unavailable = normalizeBriefUnavailable(readRecordValue(record, ["Unavailable", "unavailable"]));
      if (slot === "" && card === null && unavailable === void 0) {
        return [];
      }
      return [{
        Slot: slot,
        Card: card || void 0,
        Unavailable: unavailable
      }];
    });
  };
  var normalizeBriefDaily = (value) => {
    const record = asRecord(value);
    if (!record) {
      return void 0;
    }
    return {
      Date: normalizeParamValue(readRecordValue(record, ["Date", "date"])),
      Slots: normalizeBriefSlotResults(readRecordValue(record, ["Slots", "slots"]))
    };
  };
  var normalizeBriefViewResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      activity: normalizeBriefRollingActivity(readRecordValue(record, ["activity", "Activity"]))
    };
  };
  var normalizeBriefItemResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      action: normalizeParamValue(readRecordValue(record, ["action", "Action"])),
      card_id: normalizeParamValue(readRecordValue(record, ["card_id", "CardID", "cardId"])),
      slot: normalizeParamValue(readRecordValue(record, ["slot", "Slot"])),
      daily: normalizeBriefDaily(readRecordValue(record, ["daily", "Daily"])),
      activity: normalizeBriefRollingActivity(readRecordValue(record, ["activity", "Activity"])),
      affected: normalizeBriefSlotResults(readRecordValue(record, ["affected", "Affected"]))
    };
  };
  var normalizeBriefInsightResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      enabled: readBoolean(readRecordValue(record, ["enabled", "Enabled"])),
      card_id: normalizeParamValue(readRecordValue(record, ["card_id", "CardID", "cardId"])),
      slot: normalizeParamValue(readRecordValue(record, ["slot", "Slot"])),
      output: normalizeParamValue(readRecordValue(record, ["output", "Output"])),
      rendered_html: normalizeParamValue(
        readRecordValue(record, ["rendered_html", "RenderedHTML", "renderedHtml"])
      )
    };
  };
  var buildBriefViewForm = (request) => {
    const body = new URLSearchParams();
    body.set("token", request.token);
    return body;
  };
  var buildBriefItemForm = (request) => {
    const body = new URLSearchParams();
    body.set("card_id", request.cardID);
    body.set("action", request.action);
    body.set("token", request.token);
    return body;
  };
  var buildBriefInsightForm = (request) => {
    const body = new URLSearchParams();
    body.set("card_id", request.cardID);
    if (request.prompt.trim() !== "") {
      body.set("prompt", request.prompt.trim());
    }
    body.set("token", request.token);
    return body;
  };

  // internal/web/static_src/api-bulk.ts
  var normalizeBulkAction = (value) => {
    switch (normalizeParamValue(value)) {
      case "trash":
      case "move":
      case "frontmatter_set":
      case "frontmatter_unset":
        return normalizeParamValue(value);
      default:
        return void 0;
    }
  };
  var normalizeBulkManagementPath = (value) => {
    const record = asRecord(value) || {};
    return {
      key: normalizeParamValue(record.key),
      path: normalizeParamValue(record.path),
      rel_path: normalizeParamValue(record.rel_path)
    };
  };
  var normalizeBulkManagementItem = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    return {
      key: normalizeParamValue(record.key) || void 0,
      path: normalizeParamValue(record.path) || void 0,
      rel_path: normalizeParamValue(record.rel_path) || void 0,
      changed: readBoolean(record.changed)
    };
  };
  var normalizeBulkFailures = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const key = normalizeParamValue(record.key);
      const relPath = normalizeParamValue(record.rel_path);
      const code = normalizeParamValue(record.code);
      const message = normalizeParamValue(record.message);
      if (key === "" && relPath === "" && code === "" && message === "") {
        return [];
      }
      return [{
        key: key || void 0,
        rel_path: relPath || void 0,
        code: code || void 0,
        message: message || void 0
      }];
    });
  };
  var normalizeBulkAffectedPaths = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      return [{
        role: normalizeParamValue(record.role),
        key: normalizeParamValue(record.key),
        path: normalizeParamValue(record.path),
        rel_path: normalizeParamValue(record.rel_path),
        exists: readBoolean(record.exists)
      }];
    });
  };
  var normalizeBulkPreviewValue = (value) => {
    const record = asRecord(value);
    if (!record) {
      return { present: false };
    }
    if (!readBoolean(record.present)) {
      return { present: false };
    }
    return {
      present: true,
      value: record.value
    };
  };
  var normalizeBulkFrontmatterChanges = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const key = normalizeParamValue(record.key);
      if (key === "") {
        return [];
      }
      return [{
        key,
        before: normalizeBulkPreviewValue(record.before),
        after: normalizeBulkPreviewValue(record.after),
        changed: readBoolean(record.changed)
      }];
    });
  };
  var normalizeBulkMovePreview = (value) => {
    const record = asRecord(value);
    if (!record) {
      return void 0;
    }
    return {
      rewrite_count: readCount(record.rewrite_count),
      rewrite_file_count: readCount(record.rewrite_file_count),
      body_rewrite_count: readCount(record.body_rewrite_count),
      frontmatter_rewrite_count: readCount(record.frontmatter_rewrite_count)
    };
  };
  var normalizeExpiryString = (value) => {
    const raw = normalizeParamValue(value);
    if (raw === "") {
      return "";
    }
    const parsed = new Date(raw);
    return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
  };
  var normalizeBulkPreviewRows = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const source = normalizeBulkManagementPath(record.source);
      const destinationRecord = asRecord(record.destination);
      return [{
        key: normalizeParamValue(record.key),
        path: normalizeParamValue(record.path),
        rel_path: normalizeParamValue(record.rel_path),
        source,
        destination: destinationRecord ? normalizeBulkManagementPath(destinationRecord) : void 0,
        changed: readBoolean(record.changed),
        move: normalizeBulkMovePreview(record.move),
        frontmatter: normalizeBulkFrontmatterChanges(record.frontmatter),
        affected: normalizeBulkAffectedPaths(record.affected)
      }];
    });
  };
  var normalizeBulkNoteKeys = (keys) => {
    const out = [];
    const seen = /* @__PURE__ */ new Set();
    for (const rawKey of keys) {
      const key = normalizeParamValue(rawKey);
      if (key === "") {
        continue;
      }
      const folded = key.toLowerCase();
      if (seen.has(folded)) {
        continue;
      }
      seen.add(folded);
      out.push(key);
    }
    return out;
  };
  var buildBulkRequestForm = (request) => {
    const body = new URLSearchParams();
    body.set("action", request.action);
    body.set("token", request.token);
    for (const key of normalizeBulkNoteKeys(request.keys)) {
      body.append("keys[]", key);
    }
    if (request.folder !== void 0) {
      body.set("folder", request.folder);
    }
    if (request.property !== void 0) {
      body.set("property", request.property);
    }
    if (request.op !== void 0) {
      body.set("op", request.op);
    }
    if (request.value !== void 0) {
      body.set("value", request.value);
    }
    if (request.previewID !== void 0 && request.previewID.trim() !== "") {
      body.set("preview_id", request.previewID.trim());
    }
    return body;
  };
  var buildBulkPreviewForm = (request) => buildBulkRequestForm(request);
  var buildBulkApplyForm = (request) => buildBulkRequestForm(request);
  var normalizeBulkManagementResult = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ...base,
      changed: readCount(record.changed),
      unchanged: readCount(record.unchanged),
      failed: readCount(record.failed),
      results: Array.isArray(record.results) ? record.results.flatMap((item) => {
        const normalized = normalizeBulkManagementItem(item);
        return normalized ? [normalized] : [];
      }) : [],
      failures: normalizeBulkFailures(record.failures)
    };
  };
  var normalizeBulkPreviewManagementResult = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ...base,
      action: normalizeBulkAction(record.action),
      changed: readCount(record.changed),
      unchanged: readCount(record.unchanged),
      failed: readCount(record.failed),
      preview_id: normalizeParamValue(record.preview_id) || void 0,
      expires_at: normalizeExpiryString(record.expires_at) || void 0,
      results: normalizeBulkPreviewRows(record.results),
      failures: normalizeBulkFailures(record.failures)
    };
  };

  // internal/web/static_src/api-cockpit.ts
  var emptyCockpitCounts = () => ({
    active: 0,
    done_hidden: 0,
    snoozed_hidden: 0,
    total_generated: 0
  });
  var normalizeCockpitCounts = (value) => {
    const record = asRecord(value);
    if (!record) {
      return emptyCockpitCounts();
    }
    return {
      active: readCount(record.active),
      done_hidden: readCount(record.done_hidden),
      snoozed_hidden: readCount(record.snoozed_hidden),
      total_generated: readCount(record.total_generated)
    };
  };
  var normalizeCockpitActions = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const kind = normalizeParamValue(record.kind);
      if (kind === "") {
        return [];
      }
      return [{ kind, label: normalizeParamValue(record.label) || kind }];
    });
  };
  var normalizeCockpitAffectedNotes = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const affected = {
        title: normalizeParamValue(record.title),
        rel_path: normalizeParamValue(record.rel_path),
        folder: normalizeParamValue(record.folder) || void 0,
        location: normalizeParamValue(record.location) || void 0
      };
      if (affected.title === "" && affected.rel_path === "" && !affected.folder && !affected.location) {
        return [];
      }
      return [affected];
    });
  };
  var normalizeCockpitTasks = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const id = normalizeParamValue(record.id);
      if (id === "") {
        return [];
      }
      return [{
        id,
        signature: normalizeParamValue(record.signature),
        kind: normalizeParamValue(record.kind),
        priority: readCount(record.priority),
        title: normalizeParamValue(record.title),
        summary: normalizeParamValue(record.summary),
        detail: normalizeParamValue(record.detail),
        count: readCount(record.count),
        affected: normalizeCockpitAffectedNotes(record.affected),
        actions: normalizeCockpitActions(record.actions),
        triage_only: readBoolean(record.triage_only)
      }];
    });
  };
  var normalizeCockpitWarnings = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const message = normalizeParamValue(record.message);
      const path = normalizeParamValue(record.path);
      if (message === "" && path === "") {
        return [];
      }
      return [{ message, path: path || void 0 }];
    });
  };
  var normalizeCockpitQueueResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      tasks: normalizeCockpitTasks(record.tasks),
      counts: normalizeCockpitCounts(record.counts),
      warnings: normalizeCockpitWarnings(record.warnings)
    };
  };
  var normalizeNotePreviewText = (value) => {
    const record = asRecord(value);
    if (!record) {
      return { present: false, text: "" };
    }
    return {
      present: readBoolean(record.present),
      text: normalizeParamValue(record.text)
    };
  };
  var normalizeNotePreviewChanges = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const kind = normalizeParamValue(record.kind);
      if (kind === "") {
        return [];
      }
      return [{
        kind,
        key: normalizeParamValue(record.key) || void 0,
        line: readCount(record.line) || void 0,
        old: normalizeNotePreviewText(record.old),
        new: normalizeNotePreviewText(record.new)
      }];
    });
  };
  var normalizeNotePreviews = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      return [{
        rel_path: normalizeParamValue(record.rel_path),
        changes: normalizeNotePreviewChanges(record.changes)
      }];
    });
  };
  var normalizeFixSkips = (value) => {
    if (!Array.isArray(value)) {
      return [];
    }
    return value.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const reason = normalizeParamValue(record.reason);
      const relPath = normalizeParamValue(record.rel_path);
      const target = normalizeParamValue(record.target);
      if (reason === "" && relPath === "" && target === "") {
        return [];
      }
      return [{
        rel_path: relPath || void 0,
        target: target || void 0,
        reason
      }];
    });
  };
  var normalizeFixPreviewResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      action: normalizeParamValue(record.action),
      task_id: normalizeParamValue(record.task_id),
      previews: normalizeNotePreviews(record.previews),
      skipped: normalizeFixSkips(record.skipped),
      preview_id: normalizeParamValue(record.preview_id),
      expires_at: normalizeExpiryString(record.expires_at)
    };
  };
  var normalizeFixApplyResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      action: normalizeParamValue(record.action),
      task_id: normalizeParamValue(record.task_id),
      changed: normalizeStringArray(record.changed),
      created: normalizeStringArray(record.created),
      skipped: normalizeFixSkips(record.skipped),
      journal_id: normalizeParamValue(record.journal_id) || void 0
    };
  };
  var buildCockpitItemForm = (request) => {
    const body = new URLSearchParams();
    body.set("id", request.id);
    body.set("action", request.action);
    body.set("token", request.token);
    if (request.action === "snooze" && request.snooze) {
      body.set("snooze", request.snooze);
    }
    return body;
  };
  var buildFixPreviewForm = (request) => {
    const body = new URLSearchParams();
    body.set("id", request.id);
    body.set("action", request.action);
    body.set("token", request.token);
    return body;
  };
  var buildFixApplyForm = (request) => {
    const body = buildFixPreviewForm(request);
    if (request.previewID !== void 0 && request.previewID.trim() !== "") {
      body.set("preview_id", request.previewID.trim());
    }
    return body;
  };

  // internal/web/static_src/api-facets.ts
  var facetGroups = [
    { key: "tags", label: "Tags", param: "tag", repeat: true },
    { key: "categories", label: "Categories", param: "category", repeat: true },
    { key: "status", label: "Status", param: "status" },
    { key: "types", label: "Types", param: "type" },
    { key: "folders", label: "Folders", param: "folder" },
    { key: "properties", label: "Properties", param: "has" },
    { key: "issues", label: "Issues", issue: true }
  ];
  var labelForIssueParam = (param) => {
    switch (param) {
      case "broken":
        return "Broken links";
      case "dead_end":
        return "Dead ends";
      case "orphan":
        return "Orphans";
      default:
        return param;
    }
  };
  var fallbackGroupForParam = (param) => {
    for (const group of facetGroups) {
      if (group.issue) {
        continue;
      }
      if (group.param === param) {
        return group;
      }
    }
    if (param === "broken" || param === "dead_end" || param === "orphan") {
      return { label: "Issues", issue: true };
    }
    return { label: "Filters" };
  };
  var fallbackFacetOption = (param, value) => {
    const group = fallbackGroupForParam(param);
    const label = group.issue ? labelForIssueParam(param) : value;
    return {
      param,
      value,
      label,
      count: 0,
      groupLabel: group.label,
      repeat: repeatedFilterParams.has(param)
    };
  };
  var normalizeFacetOptions = (payload) => {
    const data = asRecord(payload) || {};
    const options = [];
    for (const group of facetGroups) {
      const rawValues = group.key ? data[group.key] : void 0;
      const values = Array.isArray(rawValues) ? rawValues : [];
      for (const item of values) {
        const record = asRecord(item);
        if (!record) {
          continue;
        }
        const param = group.issue ? normalizeParamValue(record.param) : normalizeParamValue(group.param);
        const value = normalizeParamValue(record.value);
        if (param === "" || value === "") {
          continue;
        }
        options.push({
          param,
          value,
          label: group.issue ? normalizeParamValue(record.label) || labelForIssueParam(param) : value,
          count: readCount(record.count),
          groupLabel: group.label,
          repeat: Boolean(group.repeat)
        });
      }
    }
    return options.sort((left, right) => {
      if (left.groupLabel !== right.groupLabel) {
        return left.groupLabel.localeCompare(right.groupLabel);
      }
      if (right.count !== left.count) {
        return right.count - left.count;
      }
      return left.label.localeCompare(right.label);
    });
  };

  // internal/web/static_src/api-history.ts
  var buildUndoForm = (request) => {
    const body = new URLSearchParams();
    body.set("token", request.token);
    if (request.entryID !== void 0 && request.entryID.trim() !== "") {
      body.set("id", request.entryID.trim());
    }
    if (request.force) {
      body.set("force", "1");
    }
    if (request.confirmForce) {
      body.set("confirm_force", "1");
    }
    return body;
  };
  var normalizeHistoryEntries = (payload) => {
    const data = asRecord(payload);
    const rawEntries = data && Array.isArray(data.entries) ? data.entries : [];
    return rawEntries.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const id = normalizeParamValue(record.id);
      if (id === "") {
        return [];
      }
      const rawFiles = Array.isArray(record.files) ? record.files : [];
      const files = rawFiles.flatMap((rawFile) => {
        const file = asRecord(rawFile);
        if (!file) {
          return [];
        }
        return [{
          path: normalizeParamValue(file.path),
          before: normalizeParamValue(file.before),
          after: normalizeParamValue(file.after)
        }];
      });
      return [{
        id,
        timestamp: normalizeParamValue(record.timestamp),
        command: normalizeParamValue(record.command),
        file_count: readCount(record.file_count),
        files
      }];
    });
  };

  // internal/web/static_src/api-notes.ts
  var managementMutationEventName = "bn:management:mutated";
  var normalizeNoteInsightResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      enabled: readBoolean(readRecordValue(record, ["enabled", "Enabled"])),
      key: normalizeParamValue(readRecordValue(record, ["key", "Key"])),
      output: normalizeParamValue(readRecordValue(record, ["output", "Output"])),
      rendered_html: normalizeParamValue(
        readRecordValue(record, ["rendered_html", "RenderedHTML", "renderedHtml"])
      )
    };
  };
  var buildNoteInsightForm = (request) => {
    const body = new URLSearchParams();
    body.set("key", request.key);
    if (request.prompt.trim() !== "") {
      body.set("prompt", request.prompt.trim());
    }
    body.set("token", request.token);
    return body;
  };
  var buildNoteKeyMutationForm = (request) => {
    const body = new URLSearchParams();
    body.set("key", request.key);
    body.set("token", request.token);
    return body;
  };
  var buildCreateNoteForm = (request) => {
    const body = buildNoteKeyMutationForm(request);
    body.set("target", request.target);
    return body;
  };
  var buildFrontmatterSetForm = (request) => {
    const body = buildNoteKeyMutationForm(request);
    body.set("property", request.property);
    body.set("op", request.op);
    body.set("value", request.value);
    return body;
  };
  var buildFrontmatterUnsetForm = (request) => {
    const body = buildNoteKeyMutationForm(request);
    body.set("property", request.property);
    return body;
  };
  var buildOpenNoteForm = (request) => buildNoteKeyMutationForm(request);
  var normalizeManagementErrorMessage = (status, detail) => {
    const result = normalizeManagementResult(detail);
    if (!result.ok && result.message) {
      return result.message;
    }
    const message = asRecord(detail) ? "" : normalizeParamValue(detail);
    return message || `Request failed (${status}).`;
  };
  var normalizeOpenErrorMessage = (status, detail) => {
    const message = normalizeParamValue(detail);
    return message || `Open failed (${status}).`;
  };

  // internal/web/static_src/api-search.ts
  var normalizeSearchResults = (payload) => {
    if (!Array.isArray(payload)) {
      return [];
    }
    return payload.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      return [
        {
          key: normalizeParamValue(record.key),
          path: normalizeParamValue(record.path),
          title: normalizeParamValue(record.title),
          folder: normalizeParamValue(record.folder),
          matched_fields: normalizeStringArray(record.matched_fields),
          issues: normalizeStringArray(record.issues),
          snippet: normalizeParamValue(record.snippet)
        }
      ];
    });
  };
  var searchResultPath = (result) => {
    if (!result) {
      return "";
    }
    const path = typeof result.path === "string" ? result.path.trim() : "";
    if (path.startsWith("/n/")) {
      return path;
    }
    return notePathForKey(result.key);
  };

  // internal/web/static_src/dom.ts
  var narrowElement = (element, constructor) => element instanceof constructor ? element : null;
  var queryElement = (root, selector, constructor) => narrowElement(root.querySelector(selector), constructor);
  var queryAllElements = (root, selector, constructor) => Array.from(root.querySelectorAll(selector)).flatMap((element) => {
    const narrowed = narrowElement(element, constructor);
    return narrowed ? [narrowed] : [];
  });
  var getElementByID = (id, constructor) => narrowElement(document.getElementById(id), constructor);

  // internal/web/static_src/status.ts
  var statusClassNames = ["is-success", "is-error", "is-loading", "is-empty"];
  var setStatusText = (element, message, kind = "", classNames = statusClassNames) => {
    if (!element) {
      return;
    }
    element.textContent = message;
    element.classList.remove(...classNames);
    if (kind) {
      element.classList.add(kind);
    }
  };

  // internal/web/static_src/broken-create.ts
  var readCreateNoteConfig = (container) => ({
    sourceKey: container.getAttribute("data-create-source-key") || "",
    target: container.getAttribute("data-create-target") || "",
    csrfToken: container.getAttribute("data-create-csrf") || ""
  });
  var buildCreateNoteFetchRequest = (config) => {
    const body = buildCreateNoteForm({
      key: config.sourceKey,
      target: config.target,
      token: config.csrfToken
    });
    return {
      url: "/api/notes/create",
      init: {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-BN-CSRF": config.csrfToken
        },
        body: body.toString()
      }
    };
  };
  var initBrokenCreateActions = (deps = {}) => {
    const containers = queryAllElements(document, "[data-create-note]", HTMLElement);
    if (containers.length === 0) {
      return;
    }
    const fetcher = deps.fetch || fetch;
    for (const container of containers) {
      const button = queryElement(container, "[data-create-button]", HTMLButtonElement);
      const statusEl = queryElement(container, "[data-create-status]", HTMLElement);
      if (!button || !statusEl) {
        continue;
      }
      const config = readCreateNoteConfig(container);
      const setStatus = (message, kind = "") => {
        setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
      };
      button.addEventListener("click", async () => {
        if (!config.sourceKey || !config.target || !config.csrfToken) {
          setStatus("Create action unavailable.", "is-error");
          return;
        }
        button.disabled = true;
        setStatus("Creating note...");
        try {
          const request = buildCreateNoteFetchRequest(config);
          const resp = await fetcher(request.url, request.init);
          const payload = await readCreateResponsePayload(resp);
          if (!resp.ok) {
            setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
            return;
          }
          const result = payload;
          const message = result && result.ok && result.message ? result.message : "Created note.";
          setStatus(message, "is-success");
        } catch {
          setStatus("Create failed.", "is-error");
        } finally {
          button.disabled = false;
        }
      });
    }
  };
  var readCreateResponsePayload = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };

  // internal/web/static_src/clipboard.ts
  var readCopyPayload = (button) => ({
    text: button.getAttribute("data-copy-text") || "",
    label: button.getAttribute("data-copy-label") || "Value"
  });
  var copySuccessMessage = (label) => `${label || "Value"} copied.`;
  var hasWritableClipboard = (clipboard) => Boolean(clipboard && typeof clipboard.writeText === "function");
  var initCopyControls = () => {
    const roots = queryAllElements(document, "[data-copy-controls]", HTMLElement);
    if (roots.length === 0) {
      return;
    }
    const clipboard = navigator.clipboard;
    const canWriteClipboard = hasWritableClipboard(clipboard);
    roots.forEach((root) => {
      const statusEl = queryElement(root, "[data-copy-status]", HTMLElement);
      const buttons = queryAllElements(root, "[data-copy-text]", HTMLButtonElement);
      if (!statusEl || buttons.length === 0) {
        return;
      }
      const setStatus = (message, kind = "") => {
        setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
      };
      buttons.forEach((button) => {
        button.addEventListener("click", async () => {
          const payload = readCopyPayload(button);
          if (!payload.text) {
            setStatus("Nothing to copy.", "is-error");
            return;
          }
          if (!canWriteClipboard) {
            setStatus("Clipboard unavailable.", "is-error");
            return;
          }
          button.disabled = true;
          try {
            await clipboard.writeText(payload.text);
            setStatus(copySuccessMessage(payload.label), "is-success");
          } catch {
            setStatus("Copy failed.", "is-error");
          } finally {
            button.disabled = false;
          }
        });
      });
    });
  };

  // internal/web/static_src/daily-brief-http.ts
  var jsonPostRequest = (csrfToken, body) => ({
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": csrfToken
    },
    body
  });
  var readResponsePayload = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };

  // internal/web/static_src/daily-brief-model.ts
  var readDailyBriefConfig = (root) => {
    const insightValue = (root.getAttribute("data-brief-insight-enabled") || "").toLowerCase();
    return {
      viewEndpoint: root.getAttribute("data-brief-view-endpoint") || "/api/brief/view",
      itemEndpoint: root.getAttribute("data-brief-item-endpoint") || "/api/brief/item",
      insightEndpoint: root.getAttribute("data-brief-insight-endpoint") || "/api/brief/insight",
      csrfToken: root.getAttribute("data-brief-csrf") || "",
      insightEnabled: insightValue === "true" || insightValue === "1"
    };
  };
  var slotOrder = ["continue", "rediscover", "connect"];
  var statusClasses = ["is-success", "is-error", "is-loading", "is-empty"];
  var insightPromptMaxLength = 2e3;
  var normalizeSlot = (value) => {
    switch ((value || "").toLowerCase()) {
      case "continue":
      case "rediscover":
      case "connect":
        return value.toLowerCase();
      default:
        return "";
    }
  };
  var slotLabel = (slot) => {
    switch (slot) {
      case "continue":
        return "Continue";
      case "rediscover":
        return "Rediscover";
      case "connect":
        return "Connect";
    }
  };
  var unavailableTitle = (slot) => {
    switch (slot) {
      case "continue":
        return "No substantive recent note";
      case "rediscover":
        return "No rediscover candidate";
      case "connect":
        return "No valid connection";
    }
  };
  var relationshipLabel = (relationship) => {
    switch ((relationship || "").toLowerCase()) {
      case "inbound":
        return "Inbound link";
      case "outbound":
        return "Outbound link";
      case "shared-neighbor":
        return "Shared neighbor";
      default:
        return relationship;
    }
  };
  var briefNoteKeyFromRelPath = (relPath) => {
    const trimmed = (relPath || "").trim().replace(/\\/g, "/");
    if (trimmed === "") {
      return "";
    }
    const withoutExt = trimmed.replace(/\.md$/i, "");
    const segments = [];
    for (const segment of withoutExt.split("/")) {
      if (segment === "" || segment === "." || segment === "..") {
        return "";
      }
      segments.push(segment.toLowerCase());
    }
    return segments.join("/");
  };
  var briefNotePathForRelPath = (relPath) => {
    const key = briefNoteKeyFromRelPath(relPath);
    return key ? notePathForKey(key) : "";
  };
  var insightDisclosureMessage = [
    "Ask AI sends this card's context and your optional prompt to your configured [agent].command.",
    "BrainNotes does not store the prompt or AI output. Continue?"
  ].join("\n");

  // internal/web/static_src/daily-brief-render.ts
  var findSlotElement = (root, slot) => queryElement(root, `[data-brief-slot="${slot}"]`, HTMLElement);
  var findSlotStatusElement = (root, slot) => queryElement(root, `[data-brief-slot-status="${slot}"]`, HTMLElement);
  var markButtonDefaultDisabled = (button) => {
    button.setAttribute("data-brief-disabled-default", button.disabled ? "true" : "false");
  };
  var applyButtonDisabledDefaults = (container) => {
    for (const button of queryAllElements(container, "[data-brief-action]", HTMLButtonElement)) {
      markButtonDefaultDisabled(button);
    }
    for (const prompt of queryAllElements(container, "[data-brief-insight-prompt]", HTMLTextAreaElement)) {
      prompt.setAttribute("data-brief-disabled-default", prompt.disabled ? "true" : "false");
    }
  };
  var createActionButton = (doc, slot, cardID, action, label, enabled) => {
    const button = doc.createElement("button");
    button.type = "button";
    button.className = "brief-action-button";
    button.setAttribute("data-brief-action", action);
    button.setAttribute(`data-brief-action-${action}`, "");
    button.setAttribute("data-brief-slot", slot);
    button.setAttribute("data-brief-card-id", cardID);
    button.textContent = label;
    button.disabled = !enabled;
    button.setAttribute("data-brief-disabled-default", enabled ? "false" : "true");
    return button;
  };
  var createInsightPrompt = (doc, slot, cardID, enabled) => {
    const field = doc.createElement("div");
    field.className = "brief-insight-prompt-field";
    const promptID = `brief-insight-prompt-${slot}-${cardID}`;
    const label = doc.createElement("label");
    label.className = "brief-insight-prompt-label";
    label.htmlFor = promptID;
    label.textContent = "Ask AI about this note";
    const controls = doc.createElement("div");
    controls.className = "brief-insight-prompt-controls";
    const prompt = doc.createElement("textarea");
    prompt.id = promptID;
    prompt.className = "brief-insight-prompt";
    prompt.rows = 2;
    prompt.maxLength = insightPromptMaxLength;
    prompt.placeholder = "Optional: ask a question or specify the output";
    prompt.disabled = !enabled;
    prompt.setAttribute("data-brief-insight-prompt", "");
    prompt.setAttribute("data-brief-disabled-default", enabled ? "false" : "true");
    const button = createActionButton(doc, slot, cardID, "insight", "Ask AI", enabled);
    controls.append(prompt, button);
    const hint = doc.createElement("span");
    hint.className = "brief-insight-prompt-hint";
    hint.textContent = "Leave blank for the suggested insight. Press Ctrl/\u2318 + Enter to ask.";
    field.append(label, controls, hint);
    return field;
  };
  var ensureInsightPanel = (cardEl, slot) => {
    const existing = queryElement(cardEl, "[data-brief-insight-panel]", HTMLElement);
    if (existing) {
      return existing;
    }
    const doc = cardEl.ownerDocument;
    const panel = doc.createElement("section");
    panel.className = "brief-insight";
    panel.setAttribute("data-brief-insight-panel", "");
    panel.hidden = true;
    const header = doc.createElement("div");
    header.className = "brief-insight-header";
    const title = doc.createElement("h5");
    title.className = "brief-insight-title";
    title.textContent = "AI insight";
    header.appendChild(title);
    const copy = doc.createElement("button");
    copy.className = "brief-insight-copy";
    copy.type = "button";
    copy.hidden = true;
    copy.disabled = true;
    copy.textContent = "Copy Markdown";
    copy.setAttribute("data-brief-insight-copy", "");
    copy.setAttribute("data-brief-slot", slot);
    header.appendChild(copy);
    const body = doc.createElement("div");
    body.className = "brief-insight-body";
    body.setAttribute("data-brief-insight-body", "");
    panel.append(header, body);
    cardEl.appendChild(panel);
    return panel;
  };
  var renderInsight = (root, slot, cardID, renderedHTML, output) => {
    const slotEl = findSlotElement(root, slot);
    if (!slotEl) {
      return;
    }
    const cardEl = queryElement(
      slotEl,
      `[data-brief-card][data-brief-card-id="${cardID}"]`,
      HTMLElement
    );
    if (!cardEl) {
      return;
    }
    const panel = ensureInsightPanel(cardEl, slot);
    const body = queryElement(panel, "[data-brief-insight-body]", HTMLElement);
    const copy = queryElement(panel, "[data-brief-insight-copy]", HTMLButtonElement);
    if (!body || !copy) {
      return;
    }
    body.innerHTML = renderedHTML;
    copy.setAttribute("data-brief-copy-output", output);
    copy.hidden = false;
    copy.disabled = output.trim() === "";
    panel.hidden = false;
  };
  var buildUnavailableHint = (availability, sourcePath) => {
    if (availability.stateUnreadable) {
      return {
        open: "Open is unavailable until brief state is readable.",
        another: "Another is unavailable until brief state is readable.",
        insight: "Ask AI is unavailable until brief state is readable."
      };
    }
    return {
      open: sourcePath === "" ? "Open is unavailable because this note route could not be resolved." : availability.editorConfigured ? "" : "Open is disabled until the editor is configured.",
      another: "",
      insight: availability.insightConfigured ? "" : "Ask AI is unavailable until [agent].command is configured."
    };
  };
  var renderCard = (doc, availability, slot, card) => {
    const cardEl = doc.createElement("div");
    cardEl.className = "brief-card";
    cardEl.setAttribute("data-brief-card", "");
    cardEl.setAttribute("data-brief-card-id", card.ID);
    const sourceTitle = card.Source.Title || card.Source.RelPath;
    const sourcePath = briefNotePathForRelPath(card.Source.RelPath);
    const heading = doc.createElement("h4");
    heading.className = "brief-card-title";
    let headingLink = null;
    if (slot === "connect" && card.Candidate) {
      const candidatePath = briefNotePathForRelPath(card.Candidate.RelPath);
      const candidateTitle = card.Candidate.Title || card.Candidate.RelPath;
      if (candidatePath) {
        headingLink = doc.createElement("a");
        headingLink.textContent = candidateTitle;
        headingLink.href = candidatePath;
      } else {
        heading.textContent = candidateTitle;
      }
    } else if (sourcePath) {
      headingLink = doc.createElement("a");
      headingLink.textContent = sourceTitle;
      headingLink.href = sourcePath;
    } else {
      heading.textContent = sourceTitle;
    }
    if (headingLink) {
      heading.appendChild(headingLink);
    }
    cardEl.appendChild(heading);
    const path = doc.createElement("p");
    path.className = "brief-card-path";
    const pathCode = doc.createElement("code");
    pathCode.textContent = slot === "connect" && card.Candidate ? card.Candidate.RelPath : card.Source.RelPath;
    path.appendChild(pathCode);
    cardEl.appendChild(path);
    const explanation = doc.createElement("p");
    explanation.className = "brief-card-explanation";
    explanation.textContent = card.Explanation;
    cardEl.appendChild(explanation);
    const relationship = relationshipLabel(card.Metadata.Relationship);
    if (slot === "connect" && relationship) {
      const relationshipEl = doc.createElement("p");
      relationshipEl.className = "brief-card-meta";
      relationshipEl.textContent = `Relationship: ${relationship}`;
      cardEl.appendChild(relationshipEl);
    }
    if (slot === "connect" && card.Metadata.Bridge) {
      const bridgeEl = doc.createElement("p");
      bridgeEl.className = "brief-card-meta";
      const bridgeCode = doc.createElement("code");
      bridgeCode.textContent = card.Metadata.Bridge;
      bridgeEl.append("Bridge: ", bridgeCode);
      cardEl.appendChild(bridgeEl);
    }
    if (card.Excerpt) {
      const excerpt = doc.createElement("blockquote");
      excerpt.className = "brief-card-excerpt";
      excerpt.textContent = card.Excerpt;
      cardEl.appendChild(excerpt);
    }
    const reasons = card.Metadata.Reasons.filter((reason) => reason.trim() !== "" && reason !== card.Explanation);
    if (reasons.length > 0) {
      const list = doc.createElement("ul");
      list.className = "brief-card-reasons";
      for (const reason of reasons) {
        const item = doc.createElement("li");
        item.textContent = reason;
        list.appendChild(item);
      }
      cardEl.appendChild(list);
    }
    const unavailable = buildUnavailableHint(availability, sourcePath);
    const actionRow = doc.createElement("div");
    actionRow.className = "brief-card-actions";
    actionRow.append(
      createActionButton(doc, slot, card.ID, "open", "Open", unavailable.open === ""),
      createActionButton(doc, slot, card.ID, "another", "Another", unavailable.another === "")
    );
    cardEl.appendChild(actionRow);
    cardEl.appendChild(createInsightPrompt(doc, slot, card.ID, unavailable.insight === ""));
    if (unavailable.open) {
      const hint = doc.createElement("p");
      hint.className = "brief-action-hint";
      hint.textContent = unavailable.open;
      cardEl.appendChild(hint);
    }
    if (unavailable.another) {
      const hint = doc.createElement("p");
      hint.className = "brief-action-hint";
      hint.textContent = unavailable.another;
      cardEl.appendChild(hint);
    }
    if (unavailable.insight) {
      const hint = doc.createElement("p");
      hint.className = "brief-action-hint";
      hint.textContent = unavailable.insight;
      cardEl.appendChild(hint);
    }
    ensureInsightPanel(cardEl, slot);
    return cardEl;
  };

  // internal/web/static_src/daily-brief-actions.ts
  var readButtonSlot = (button) => normalizeSlot(button.getAttribute("data-brief-slot") || "");
  var readButtonCardID = (button) => button.getAttribute("data-brief-card-id") || "";
  var runOpen = async (ctx, button) => {
    const slot = readButtonSlot(button);
    const cardID = readButtonCardID(button);
    if (!slot || !cardID || !ctx.config.csrfToken) {
      ctx.setGlobalStatus("Open action unavailable.", "is-error");
      return;
    }
    ctx.setSlotsBusy([slot], true);
    ctx.setSlotStatus(slot, "Opening editor...", "is-loading");
    try {
      const body = buildBriefItemForm({ cardID, action: "open", token: ctx.config.csrfToken }).toString();
      const resp = await ctx.fetcher(ctx.config.itemEndpoint, jsonPostRequest(ctx.config.csrfToken, body));
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        ctx.setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefItemResponse(payload);
      if (!result.ok) {
        ctx.setSlotStatus(slot, result.message || "Open failed.", "is-error");
        return;
      }
      ctx.updateActivity(result.activity);
      ctx.openedCards.add(cardID);
      ctx.markCardOpened(slot, cardID);
      ctx.setSlotStatus(slot, "Opened in editor. Refresh after saving changes.", "is-success");
      ctx.setGlobalStatus("Brief activity updated.", "is-success");
    } catch {
      ctx.setSlotStatus(slot, "Open failed.", "is-error");
    } finally {
      ctx.setSlotsBusy([slot], false);
    }
  };
  var runAnother = async (ctx, button) => {
    const slot = readButtonSlot(button);
    const cardID = readButtonCardID(button);
    if (!slot || !cardID || !ctx.config.csrfToken) {
      ctx.setGlobalStatus("Another action unavailable.", "is-error");
      return;
    }
    const busySlots = slot === "continue" ? ["continue", "connect"] : [slot];
    ctx.setSlotsBusy(busySlots, true);
    ctx.setSlotStatus(slot, "Finding another card...", "is-loading");
    try {
      const body = buildBriefItemForm({ cardID, action: "another", token: ctx.config.csrfToken }).toString();
      const resp = await ctx.fetcher(ctx.config.itemEndpoint, jsonPostRequest(ctx.config.csrfToken, body));
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        ctx.setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefItemResponse(payload);
      if (!result.ok) {
        ctx.setSlotStatus(slot, result.message || "Another failed.", "is-error");
        return;
      }
      ctx.updateActivity(result.activity);
      const changedSlots = ctx.applyAnotherPayload(result, slot);
      const statusMessage = result.message || (slot === "continue" && changedSlots.includes("connect") ? "Updated Continue and Connect." : `Updated ${slotLabel(slot)}.`);
      ctx.setSlotStatus(slot, statusMessage, "is-success");
      ctx.setGlobalStatus(statusMessage, "is-success");
      const slotEl = findSlotElement(ctx.root, slot);
      const nextFocus = slotEl && queryElement(slotEl, "[data-brief-action-another]", HTMLButtonElement) || slotEl && queryElement(slotEl, "[data-brief-action]", HTMLButtonElement);
      nextFocus?.focus({ preventScroll: true });
    } catch {
      ctx.setSlotStatus(slot, "Another failed.", "is-error");
    } finally {
      ctx.setSlotsBusy(busySlots, false);
    }
  };
  var runInsight = async (ctx, button) => {
    const slot = readButtonSlot(button);
    const cardID = readButtonCardID(button);
    if (!slot || !cardID || !ctx.config.csrfToken) {
      ctx.setGlobalStatus("Ask AI is unavailable.", "is-error");
      return;
    }
    const cardEl = button.closest("[data-brief-card]");
    const prompt = cardEl ? queryElement(cardEl, "[data-brief-insight-prompt]", HTMLTextAreaElement)?.value.trim() || "" : "";
    if (!ctx.insightDisclosureAccepted) {
      ctx.insightDisclosureAccepted = ctx.confirmer(insightDisclosureMessage);
      if (!ctx.insightDisclosureAccepted) {
        ctx.setSlotStatus(slot, "Ask AI canceled.", "is-empty");
        return;
      }
    }
    ctx.setSlotsBusy([slot], true);
    ctx.setSlotStatus(slot, "Generating insight...", "is-loading");
    const requestID = ctx.insightCounter + 1;
    ctx.insightCounter = requestID;
    ctx.inflightInsight.set(slot, requestID);
    try {
      const body = buildBriefInsightForm({ cardID, prompt, token: ctx.config.csrfToken }).toString();
      const resp = await ctx.fetcher(
        ctx.config.insightEndpoint,
        jsonPostRequest(ctx.config.csrfToken, body)
      );
      const payload = await readResponsePayload(resp);
      if (ctx.inflightInsight.get(slot) !== requestID) {
        return;
      }
      if (!resp.ok) {
        ctx.setSlotStatus(slot, normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefInsightResponse(payload);
      if (!result.ok) {
        ctx.setSlotStatus(slot, result.message || "Ask AI failed.", "is-error");
        return;
      }
      if (!result.enabled) {
        ctx.setSlotStatus(slot, result.message || "Ask AI is unavailable.", "is-error");
        return;
      }
      if (result.card_id && result.card_id !== cardID) {
        ctx.setSlotStatus(slot, "Card changed while insight was running. Try again.", "is-error");
        return;
      }
      renderInsight(ctx.root, slot, cardID, result.rendered_html, result.output);
      ctx.setSlotStatus(slot, "Insight ready.", "is-success");
    } catch {
      if (ctx.inflightInsight.get(slot) === requestID) {
        ctx.setSlotStatus(slot, "Ask AI failed.", "is-error");
      }
    } finally {
      if (ctx.inflightInsight.get(slot) === requestID) {
        ctx.inflightInsight.delete(slot);
        ctx.setSlotsBusy([slot], false);
      }
    }
  };
  var runCopyInsight = async (ctx, button) => {
    const slot = normalizeSlot(button.getAttribute("data-brief-slot") || "");
    if (!slot) {
      return;
    }
    const payload = button.getAttribute("data-brief-copy-output") || "";
    if (payload.trim() === "") {
      ctx.setSlotStatus(slot, "Nothing to copy.", "is-error");
      return;
    }
    if (!ctx.clipboardWrite) {
      ctx.setSlotStatus(slot, "Clipboard unavailable.", "is-error");
      return;
    }
    button.disabled = true;
    try {
      await ctx.clipboardWrite(payload);
      ctx.setSlotStatus(slot, "Insight markdown copied.", "is-success");
    } catch {
      ctx.setSlotStatus(slot, "Copy failed.", "is-error");
    } finally {
      button.disabled = false;
    }
  };
  var recordView = async (ctx) => {
    if (!ctx.config.csrfToken) {
      return;
    }
    try {
      const body = buildBriefViewForm({ token: ctx.config.csrfToken }).toString();
      const resp = await ctx.fetcher(ctx.config.viewEndpoint, jsonPostRequest(ctx.config.csrfToken, body));
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        ctx.setGlobalStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBriefViewResponse(payload);
      if (!result.ok) {
        ctx.setGlobalStatus(result.message || "Brief activity unavailable.", "is-error");
        return;
      }
      ctx.updateActivity(result.activity);
    } catch {
      ctx.setGlobalStatus("Brief activity unavailable.", "is-error");
    }
  };

  // internal/web/static_src/daily-brief.ts
  var initDailyBrief = (deps = {}) => {
    const root = queryElement(document, "[data-brief-root]", HTMLElement);
    if (!root) {
      return;
    }
    const config = readDailyBriefConfig(root);
    const fetcher = deps.fetch || fetch;
    const confirmer = deps.confirm || ((message) => window.confirm(message));
    const clipboardWrite = deps.writeClipboard || (navigator.clipboard && typeof navigator.clipboard.writeText === "function" ? (text) => navigator.clipboard.writeText(text) : void 0);
    const globalStatusEl = queryElement(root, "#brief-global-status", HTMLElement);
    const visitDaysEl = queryElement(root, "[data-brief-activity-visit-days]", HTMLElement);
    const openCountEl = queryElement(root, "[data-brief-activity-open-count]", HTMLElement);
    const slotElementFor = (slot) => findSlotElement(root, slot);
    const setGlobalStatus = (message, kind = "") => {
      setStatusText(globalStatusEl, message || " ", kind, statusClasses);
    };
    const setSlotStatus = (slot, message, kind = "") => {
      setStatusText(findSlotStatusElement(root, slot), message || " ", kind, statusClasses);
    };
    const readAvailability = () => ({
      stateUnreadable: queryElement(root, "[data-brief-state-unreadable]", HTMLElement) !== null,
      editorConfigured: queryElement(root, "[data-brief-editor-unconfigured]", HTMLElement) === null,
      insightConfigured: config.insightEnabled
    });
    applyButtonDisabledDefaults(root);
    const openedCards = /* @__PURE__ */ new Set();
    const inflightInsight = /* @__PURE__ */ new Map();
    const setSlotBusy = (slot, busy) => {
      const slotEl = slotElementFor(slot);
      if (!slotEl) {
        return;
      }
      slotEl.setAttribute("aria-busy", busy ? "true" : "false");
      slotEl.classList.toggle("is-busy", busy);
      for (const button of queryAllElements(slotEl, "[data-brief-action]", HTMLButtonElement)) {
        const defaultDisabled = button.getAttribute("data-brief-disabled-default") === "true";
        button.disabled = busy || defaultDisabled;
      }
      for (const prompt of queryAllElements(slotEl, "[data-brief-insight-prompt]", HTMLTextAreaElement)) {
        const defaultDisabled = prompt.getAttribute("data-brief-disabled-default") === "true";
        prompt.disabled = busy || defaultDisabled;
      }
    };
    const setSlotsBusy = (slots, busy) => {
      for (const slot of slots) {
        setSlotBusy(slot, busy);
      }
    };
    const markCardOpened = (slot, cardID) => {
      const slotEl = slotElementFor(slot);
      if (!slotEl) {
        return;
      }
      const openButton2 = queryElement(
        slotEl,
        `[data-brief-action-open][data-brief-card-id="${cardID}"]`,
        HTMLButtonElement
      );
      if (!openButton2) {
        return;
      }
      openButton2.textContent = "Opened";
      openButton2.classList.add("is-opened");
    };
    const updateActivity = (activity) => {
      if (visitDaysEl) {
        visitDaysEl.textContent = String(activity.Last7Days.VisitDays);
      }
      if (openCountEl) {
        openCountEl.textContent = String(activity.Last7Days.OpenCount);
      }
    };
    const replaceSlotContent = (slotResult) => {
      const slot = normalizeSlot(slotResult.Slot);
      if (!slot) {
        return;
      }
      const slotEl = slotElementFor(slot);
      if (!slotEl) {
        return;
      }
      inflightInsight.delete(slot);
      const header = queryElement(slotEl, ".panel-header", HTMLElement);
      if (!header) {
        return;
      }
      while (header.nextSibling) {
        slotEl.removeChild(header.nextSibling);
      }
      if (slotResult.Card) {
        slotEl.appendChild(renderCard(root.ownerDocument, readAvailability(), slot, slotResult.Card));
        if (openedCards.has(slotResult.Card.ID)) {
          markCardOpened(slot, slotResult.Card.ID);
        }
      } else {
        const unavailableMessage = slotResult.Unavailable?.Message || `${slotLabel(slot)} is unavailable for the current brief.`;
        const title = unavailableTitle(slot);
        const h4 = slotEl.ownerDocument.createElement("h4");
        h4.textContent = title;
        slotEl.appendChild(h4);
        const empty = slotEl.ownerDocument.createElement("p");
        empty.className = "review-empty brief-slot-empty";
        empty.setAttribute("data-brief-slot-empty", slot);
        empty.setAttribute("role", "status");
        empty.textContent = unavailableMessage;
        slotEl.appendChild(empty);
      }
      const status = slotEl.ownerDocument.createElement("p");
      status.className = "review-status brief-slot-status";
      status.setAttribute("data-brief-slot-status", slot);
      status.setAttribute("role", "status");
      status.setAttribute("aria-live", "polite");
      status.textContent = " ";
      slotEl.appendChild(status);
      applyButtonDisabledDefaults(slotEl);
    };
    const applyAnotherPayload = (result, requestedSlot) => {
      const changed = /* @__PURE__ */ new Set();
      for (const slotResult of result.affected) {
        const slot = normalizeSlot(slotResult.Slot);
        if (!slot) {
          continue;
        }
        replaceSlotContent(slotResult);
        changed.add(slot);
      }
      if (result.daily && result.daily.Slots.length > 0) {
        const ensureSlot = (slot) => {
          if (changed.has(slot)) {
            return;
          }
          const match = result.daily?.Slots.find((candidate) => normalizeSlot(candidate.Slot) === slot);
          if (!match) {
            return;
          }
          replaceSlotContent(match);
          changed.add(slot);
        };
        ensureSlot(requestedSlot);
        if (requestedSlot === "continue") {
          ensureSlot("connect");
        }
      }
      return Array.from(changed);
    };
    const ctx = {
      root,
      config,
      fetcher,
      confirmer,
      clipboardWrite,
      openedCards,
      inflightInsight,
      insightCounter: 0,
      insightDisclosureAccepted: false,
      setGlobalStatus,
      setSlotStatus,
      setSlotsBusy,
      updateActivity,
      markCardOpened,
      applyAnotherPayload
    };
    root.addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const copy = target.closest("[data-brief-insight-copy]");
      if (copy && root.contains(copy)) {
        void runCopyInsight(ctx, copy);
        return;
      }
      const open = target.closest("[data-brief-action-open]");
      if (open && root.contains(open)) {
        void runOpen(ctx, open);
        return;
      }
      const another = target.closest("[data-brief-action-another]");
      if (another && root.contains(another)) {
        void runAnother(ctx, another);
        return;
      }
      const insight = target.closest("[data-brief-action-insight]");
      if (insight && root.contains(insight)) {
        void runInsight(ctx, insight);
      }
    });
    root.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || !event.ctrlKey && !event.metaKey) {
        return;
      }
      const target = event.target;
      if (!(target instanceof HTMLTextAreaElement) || !target.matches("[data-brief-insight-prompt]")) {
        return;
      }
      const cardEl = target.closest("[data-brief-card]");
      const button = cardEl && queryElement(cardEl, "[data-brief-action-insight]", HTMLButtonElement);
      if (!button || button.disabled) {
        return;
      }
      event.preventDefault();
      void runInsight(ctx, button);
    });
    for (const slot of slotOrder) {
      const slotEl = slotElementFor(slot);
      if (!slotEl) {
        continue;
      }
      const cardEl = queryElement(slotEl, "[data-brief-card]", HTMLElement);
      if (!cardEl) {
        continue;
      }
      ensureInsightPanel(cardEl, slot);
    }
    void recordView(ctx);
  };

  // internal/web/static_src/daily-fixes-model.ts
  var FIX_ACTIONS = /* @__PURE__ */ new Set(["schema_repair", "relink", "create_stub"]);
  var readDailyFixesConfig = (root) => ({
    queueEndpoint: root.getAttribute("data-fixes-endpoint") || "/api/cockpit",
    itemEndpoint: root.getAttribute("data-fixes-item-endpoint") || "/api/cockpit/item",
    previewEndpoint: root.getAttribute("data-fixes-preview-endpoint") || "/api/fixes/preview",
    applyEndpoint: root.getAttribute("data-fixes-apply-endpoint") || "/api/fixes/apply",
    openEndpoint: root.getAttribute("data-fixes-open-endpoint") || "/api/open",
    csrfToken: root.getAttribute("data-fixes-csrf") || ""
  });
  var fixOpenKeyFromRelPath = (relPath) => {
    const trimmed = (relPath || "").trim().replace(/\\/g, "/");
    if (trimmed === "") {
      return "";
    }
    const withoutExt = trimmed.replace(/\.md$/i, "");
    const segments = [];
    for (const segment of withoutExt.split("/")) {
      if (segment === "" || segment === "." || segment === "..") {
        return "";
      }
      segments.push(segment.toLowerCase());
    }
    return segments.join("/");
  };
  var fixKindLabel = (kind) => {
    switch (kind) {
      case "frontmatter_error":
        return "Frontmatter";
      case "schema_violation":
        return "Schema";
      case "broken_target":
        return "Broken target";
      case "duplicate_title":
        return "Duplicate title";
      case "orphan_note":
        return "Orphan";
      case "dead_end_note":
        return "Dead end";
      case "long_tail_value":
        return "Long-tail value";
      case "sensitive_content":
        return "Sensitive content";
      case "missing_review_date":
        return "Missing review date";
      case "stale_status":
        return "Stale status";
      case "wip_limit":
        return "WIP limit";
      case "hub_needs_synthesis":
        return "Hub needs synthesis";
      case "ai_daily_review":
        return "AI daily review";
      default:
        return "Fix";
    }
  };
  var fixTaskLabel = (task) => task.title || task.summary || task.id || "Fix";
  var affectedNoteLabel = (note) => note.title || note.rel_path || "Affected note";
  var fixCountsStatusMessage = (counts) => {
    if (counts.active === 1) {
      return "1 active fix.";
    }
    if (counts.active > 1) {
      return `${counts.active} active fixes.`;
    }
    return "No active fixes.";
  };
  var normalizeItemAction = (value) => {
    switch (value) {
      case "mark_done":
      case "done":
      case "snooze":
      case "clear":
        return value;
      default:
        return "";
    }
  };
  var normalizeSnooze = (value) => {
    switch (value) {
      case "tomorrow":
      case "week":
        return value;
      default:
        return void 0;
    }
  };
  var normalizeFixAction = (value) => {
    switch (value) {
      case "schema_repair":
      case "relink":
      case "create_stub":
        return value;
      default:
        return "";
    }
  };
  var itemActionLoadingMessage = (action, snooze) => {
    if (action === "snooze") {
      return snooze === "week" ? "Snoozing fix for a week..." : "Snoozing fix until tomorrow...";
    }
    if (action === "clear") {
      return "Clearing fix state...";
    }
    return "Marking fix done...";
  };

  // internal/web/static_src/daily-fixes-preview.ts
  var previewChangeText = (value) => value.present ? value.text === "" ? "(empty)" : value.text : "(absent)";
  var previewChangeLine = (change) => {
    const rendered = `${previewChangeText(change.old)} -> ${previewChangeText(change.new)}`;
    switch (change.kind) {
      case "frontmatter":
        return `frontmatter[${change.key || ""}]: ${rendered}`;
      case "body":
        return `body:${change.line || 0}: ${rendered}`;
      case "path":
        return `path: ${rendered}`;
      default:
        return rendered;
    }
  };
  var findPreviewEl = (listEl, id) => {
    for (const task of queryAllElements(listEl, "[data-fixes-task]", HTMLElement)) {
      if (task.getAttribute("data-fixes-id") === id) {
        return queryElement(task, "[data-fixes-preview]", HTMLElement);
      }
    }
    return null;
  };
  var renderPreview = (listEl, id, action, previews_, message) => {
    const el = findPreviewEl(listEl, id);
    if (!el) {
      return;
    }
    const doc = el.ownerDocument;
    el.textContent = "";
    el.hidden = false;
    const heading = doc.createElement("p");
    heading.className = "fixes-preview-message";
    heading.textContent = message || "Preview ready.";
    el.appendChild(heading);
    for (const preview of previews_) {
      const noteEl = doc.createElement("div");
      noteEl.className = "fixes-preview-note";
      const path = doc.createElement("p");
      path.className = "fixes-preview-path";
      path.textContent = preview.rel_path;
      noteEl.appendChild(path);
      const changeList = doc.createElement("ul");
      changeList.className = "fixes-preview-changes";
      for (const change of preview.changes) {
        const line = doc.createElement("li");
        line.textContent = previewChangeLine(change);
        changeList.appendChild(line);
      }
      noteEl.appendChild(changeList);
      el.appendChild(noteEl);
    }
    const controls = doc.createElement("div");
    controls.className = "fixes-preview-actions";
    const applyBtn = doc.createElement("button");
    applyBtn.className = "review-action fixes-preview-apply";
    applyBtn.type = "button";
    applyBtn.setAttribute("data-fixes-button", "");
    applyBtn.setAttribute("data-fixes-preview-apply", "");
    applyBtn.setAttribute("data-fixes-id", id);
    applyBtn.textContent = "Apply fix";
    const cancelBtn = doc.createElement("button");
    cancelBtn.className = "review-action fixes-preview-cancel";
    cancelBtn.type = "button";
    cancelBtn.setAttribute("data-fixes-button", "");
    cancelBtn.setAttribute("data-fixes-preview-cancel", "");
    cancelBtn.setAttribute("data-fixes-id", id);
    cancelBtn.textContent = "Cancel";
    controls.append(applyBtn, cancelBtn);
    el.appendChild(controls);
    void action;
  };
  var clearPreviewEl = (listEl, id) => {
    const el = findPreviewEl(listEl, id);
    if (el) {
      el.textContent = "";
      el.hidden = true;
    }
  };

  // internal/web/static_src/daily-fixes-requests.ts
  var jsonPostRequest2 = (url, csrfToken, body) => ({
    url,
    init: {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": csrfToken
      },
      body
    }
  });
  var buildFixesItemFetchRequest = (endpoint, csrfToken, id, action, snooze) => {
    const body = buildCockpitItemForm({ id, action, token: csrfToken, snooze });
    return jsonPostRequest2(endpoint, csrfToken, body.toString());
  };
  var buildFixesPreviewFetchRequest = (endpoint, csrfToken, id, action) => {
    const body = buildFixPreviewForm({ id, action, token: csrfToken });
    return jsonPostRequest2(endpoint, csrfToken, body.toString());
  };
  var buildFixesApplyFetchRequest = (endpoint, csrfToken, id, action, previewID) => {
    const body = buildFixApplyForm({ id, action, token: csrfToken, previewID });
    return jsonPostRequest2(endpoint, csrfToken, body.toString());
  };
  var readResponsePayload2 = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };

  // internal/web/static_src/daily-fixes-render.ts
  var renderCounts = (countEls, counts) => {
    if (countEls.active) {
      countEls.active.textContent = String(counts.active);
    }
    if (countEls.doneHidden) {
      countEls.doneHidden.textContent = String(counts.done_hidden);
    }
    if (countEls.snoozedHidden) {
      countEls.snoozedHidden.textContent = String(counts.snoozed_hidden);
    }
    if (countEls.totalGenerated) {
      countEls.totalGenerated.textContent = String(counts.total_generated);
    }
  };
  var renderWarnings = (warningListEl, result) => {
    if (!warningListEl) {
      return;
    }
    warningListEl.textContent = "";
    warningListEl.hidden = result.warnings.length === 0;
    for (const warning of result.warnings) {
      const item = warningListEl.ownerDocument.createElement("li");
      item.setAttribute("data-fixes-warning", "");
      item.textContent = warning.message || "Cockpit warning.";
      warningListEl.appendChild(item);
    }
  };
  var openButton = (doc, note) => {
    const key = fixOpenKeyFromRelPath(note.rel_path);
    if (key === "") {
      return null;
    }
    const button = doc.createElement("button");
    button.className = "review-action review-open-editor";
    button.type = "button";
    button.setAttribute("data-fixes-button", "");
    button.setAttribute("data-fixes-open-key", key);
    button.setAttribute("aria-label", `Open ${affectedNoteLabel(note)} in editor`);
    button.textContent = "Open in editor";
    return button;
  };
  var renderAffectedNote = (doc, list, note) => {
    const item = doc.createElement("li");
    item.className = "review-affected-note";
    const label = affectedNoteLabel(note);
    const key = fixOpenKeyFromRelPath(note.rel_path);
    if (key !== "") {
      const link = doc.createElement("a");
      link.href = notePathForKey(key);
      link.textContent = label;
      item.appendChild(link);
    } else {
      const text = doc.createElement("span");
      text.textContent = label;
      item.appendChild(text);
    }
    if (note.folder) {
      const folder = doc.createElement("span");
      folder.className = "diagnostic-folder";
      folder.textContent = note.folder;
      item.appendChild(folder);
    }
    if (note.location) {
      const location = doc.createElement("span");
      location.className = "diagnostic-location";
      location.textContent = note.location;
      item.appendChild(location);
    }
    const open = openButton(doc, note);
    if (open) {
      item.appendChild(open);
    }
    list.appendChild(item);
  };
  var triageButton = (doc, id, action, label, snooze) => {
    const button = doc.createElement("button");
    button.className = "review-action";
    button.type = "button";
    button.setAttribute("data-fixes-button", "");
    button.setAttribute("data-fixes-item-action", action);
    button.setAttribute("data-fixes-id", id);
    if (snooze) {
      button.setAttribute("data-fixes-snooze", snooze);
    }
    button.textContent = label;
    return button;
  };
  var fixButton = (doc, id, action, label) => {
    const button = doc.createElement("button");
    button.className = "review-action fixes-fix-action";
    button.type = "button";
    button.setAttribute("data-fixes-button", "");
    button.setAttribute("data-fixes-fix-action", action);
    button.setAttribute("data-fixes-id", id);
    button.textContent = label;
    return button;
  };
  var renderTask = (doc, task) => {
    const row = doc.createElement("li");
    row.className = "review-item fixes-item";
    row.setAttribute("data-fixes-task", "");
    row.setAttribute("data-fixes-id", task.id);
    row.setAttribute("data-fixes-kind", task.kind);
    row.setAttribute("data-fixes-signature", task.signature);
    if (task.triage_only) {
      row.setAttribute("data-fixes-triage", "");
    }
    const main = doc.createElement("div");
    main.className = "review-item-main";
    const kind = doc.createElement("p");
    kind.className = "review-item-kind";
    kind.textContent = fixKindLabel(task.kind);
    main.appendChild(kind);
    const title = doc.createElement("h3");
    title.className = "review-item-title";
    title.textContent = fixTaskLabel(task);
    main.appendChild(title);
    if (task.summary) {
      const summary = doc.createElement("p");
      summary.className = "review-item-summary";
      summary.textContent = task.summary;
      main.appendChild(summary);
    }
    if (task.detail || task.affected.length > 0) {
      const details = doc.createElement("details");
      details.className = "review-item-details";
      const detailsSummary = doc.createElement("summary");
      detailsSummary.textContent = "View details";
      details.appendChild(detailsSummary);
      if (task.detail) {
        const detail = doc.createElement("p");
        detail.className = "review-item-detail";
        detail.textContent = task.detail;
        details.appendChild(detail);
      }
      if (task.affected.length > 0) {
        const affectedList = doc.createElement("ul");
        affectedList.className = "review-affected-list";
        affectedList.setAttribute("aria-label", "Affected notes");
        for (const affected of task.affected) {
          renderAffectedNote(doc, affectedList, affected);
        }
        details.appendChild(affectedList);
      }
      main.appendChild(details);
    }
    const actions = doc.createElement("div");
    actions.className = "review-item-actions";
    actions.setAttribute("data-fixes-task-actions", "");
    if (!task.triage_only) {
      for (const action of task.actions) {
        if (FIX_ACTIONS.has(action.kind)) {
          actions.appendChild(fixButton(doc, task.id, action.kind, action.label));
        }
      }
    }
    actions.appendChild(triageButton(doc, task.id, "mark_done", "Done"));
    actions.appendChild(triageButton(doc, task.id, "snooze", "Snooze tomorrow", "tomorrow"));
    actions.appendChild(triageButton(doc, task.id, "snooze", "Snooze next week", "week"));
    main.appendChild(actions);
    const preview = doc.createElement("div");
    preview.className = "fixes-preview";
    preview.setAttribute("data-fixes-preview", "");
    preview.hidden = true;
    main.appendChild(preview);
    row.appendChild(main);
    return row;
  };
  var renderGroups = (listEl, tasks) => {
    const doc = listEl.ownerDocument;
    listEl.textContent = "";
    const order = [];
    const groups = /* @__PURE__ */ new Map();
    for (const task of tasks) {
      const bucket = groups.get(task.kind);
      if (bucket) {
        bucket.push(task);
      } else {
        order.push(task.kind);
        groups.set(task.kind, [task]);
      }
    }
    for (const kind of order) {
      const bucket = groups.get(kind) || [];
      const group = doc.createElement("li");
      group.className = "fixes-group";
      group.setAttribute("data-fixes-group", kind);
      const heading = doc.createElement("h3");
      heading.className = "fixes-group-heading";
      heading.textContent = `${fixKindLabel(kind)} (${bucket.length})`;
      group.appendChild(heading);
      const groupList = doc.createElement("ul");
      groupList.className = "fixes-group-list";
      for (const task of bucket) {
        groupList.appendChild(renderTask(doc, task));
      }
      group.appendChild(groupList);
      listEl.appendChild(group);
    }
  };

  // internal/web/static_src/daily-fixes.ts
  var initDailyFixes = (deps = {}) => {
    const root = queryElement(document, "[data-fixes-panel]", HTMLElement);
    if (!root) {
      return;
    }
    const statusEl = queryElement(root, "[data-fixes-status]", HTMLElement);
    const loadingEl = queryElement(root, "[data-fixes-loading]", HTMLElement);
    const errorEl = queryElement(root, "[data-fixes-error]", HTMLElement);
    const emptyEl = queryElement(root, "[data-fixes-empty]", HTMLElement);
    const warningListEl = queryElement(root, "[data-fixes-warnings]", HTMLElement);
    const listEl = queryElement(root, "[data-fixes-list]", HTMLElement);
    if (!statusEl || !listEl) {
      return;
    }
    const countEls = {
      active: queryElement(root, "[data-fixes-count-active]", HTMLElement),
      doneHidden: queryElement(root, "[data-fixes-count-done-hidden]", HTMLElement),
      snoozedHidden: queryElement(root, "[data-fixes-count-snoozed-hidden]", HTMLElement),
      totalGenerated: queryElement(root, "[data-fixes-count-total-generated]", HTMLElement)
    };
    const config = readDailyFixesConfig(root);
    const fetcher = deps.fetch || fetch;
    const previews = /* @__PURE__ */ new Map();
    let busy = false;
    let ignoreNextMutation = false;
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message, kind);
    };
    const setError = (message) => {
      if (!errorEl) {
        return;
      }
      errorEl.textContent = message;
      errorEl.hidden = message === "";
    };
    const showError = (message) => {
      setError(message);
      setStatus(message, "is-error");
    };
    const setLoading = (loading) => {
      if (loadingEl) {
        loadingEl.hidden = !loading;
      }
    };
    const setBusy = (next) => {
      busy = next;
      root.setAttribute("aria-busy", next ? "true" : "false");
      for (const button of queryAllElements(root, "[data-fixes-button]", HTMLButtonElement)) {
        button.disabled = next;
      }
    };
    const renderResult = (result) => {
      previews.clear();
      renderCounts(countEls, result.counts);
      renderWarnings(warningListEl, result);
      renderGroups(listEl, result.tasks);
      const hasTasks = result.tasks.length > 0;
      listEl.hidden = !hasTasks;
      if (emptyEl) {
        emptyEl.hidden = hasTasks;
      }
      setBusy(busy);
    };
    const applyQueueResult = (payload, successMessage = "", successKind = "") => {
      const result = normalizeCockpitQueueResponse(payload);
      if (!result.ok) {
        showError(result.message || "Cockpit request failed.");
        return false;
      }
      setError("");
      renderResult(result);
      const message = successMessage || fixCountsStatusMessage(result.counts);
      const kind = successKind || (result.counts.active === 0 ? "is-empty" : "");
      setStatus(message, kind);
      return true;
    };
    const loadQueue = async (successMessage = "") => {
      setBusy(true);
      setLoading(true);
      setStatus("Loading fixes queue...", "is-loading");
      try {
        const resp = await fetcher(config.queueEndpoint, {
          headers: { Accept: "application/json" }
        });
        const payload = await readResponsePayload2(resp);
        if (!resp.ok) {
          showError(normalizeManagementErrorMessage(resp.status, payload));
          return;
        }
        applyQueueResult(payload, successMessage, successMessage ? "is-success" : "");
      } catch {
        showError("Fixes queue unavailable.");
      } finally {
        setLoading(false);
        setBusy(false);
      }
    };
    const clearPreview = (id) => {
      previews.delete(id);
      clearPreviewEl(listEl, id);
    };
    const runItemAction = async (button) => {
      const id = button.getAttribute("data-fixes-id") || "";
      const action = normalizeItemAction(button.getAttribute("data-fixes-item-action") || "");
      const snooze = normalizeSnooze(button.getAttribute("data-fixes-snooze") || "");
      if (!id || !action || !config.csrfToken) {
        showError("Fix action unavailable.");
        return;
      }
      if (action === "snooze" && !snooze) {
        showError("Fix snooze unavailable.");
        return;
      }
      setBusy(true);
      setStatus(itemActionLoadingMessage(action, snooze), "is-loading");
      try {
        const request = buildFixesItemFetchRequest(
          config.itemEndpoint,
          config.csrfToken,
          id,
          action,
          snooze
        );
        const resp = await fetcher(request.url, request.init);
        const payload = await readResponsePayload2(resp);
        if (!resp.ok) {
          showError(normalizeManagementErrorMessage(resp.status, payload));
          return;
        }
        const result = normalizeCockpitQueueResponse(payload);
        applyQueueResult(payload, result.message || "Fix updated.", "is-success");
      } catch {
        showError("Fix action failed.");
      } finally {
        setBusy(false);
      }
    };
    const runPreview = async (button) => {
      const id = button.getAttribute("data-fixes-id") || "";
      const action = normalizeFixAction(button.getAttribute("data-fixes-fix-action") || "");
      if (!id || !action || !config.csrfToken) {
        showError("Fix preview unavailable.");
        return;
      }
      setBusy(true);
      setStatus("Previewing fix...", "is-loading");
      try {
        const request = buildFixesPreviewFetchRequest(
          config.previewEndpoint,
          config.csrfToken,
          id,
          action
        );
        const resp = await fetcher(request.url, request.init);
        const payload = await readResponsePayload2(resp);
        if (!resp.ok) {
          showError(normalizeManagementErrorMessage(resp.status, payload));
          return;
        }
        const result = normalizeFixPreviewResponse(payload);
        if (!result.ok || !result.preview_id) {
          showError(result.message || "Fix preview failed.");
          return;
        }
        previews.set(id, { action, previewID: result.preview_id });
        renderPreview(listEl, id, action, result.previews, result.message || "Preview ready.");
        setError("");
        setStatus(result.message || "Preview ready.", "is-success");
      } catch {
        showError("Fix preview failed.");
      } finally {
        setBusy(false);
      }
    };
    const runApply = async (button) => {
      const id = button.getAttribute("data-fixes-id") || "";
      const state = previews.get(id);
      if (!id || !state || !config.csrfToken) {
        showError("Preview the fix before applying it.");
        return;
      }
      setBusy(true);
      setStatus("Applying fix...", "is-loading");
      try {
        const request = buildFixesApplyFetchRequest(
          config.applyEndpoint,
          config.csrfToken,
          id,
          state.action,
          state.previewID
        );
        const resp = await fetcher(request.url, request.init);
        const payload = await readResponsePayload2(resp);
        if (!resp.ok) {
          clearPreview(id);
          showError(normalizeManagementErrorMessage(resp.status, payload));
          await loadQueue();
          return;
        }
        const result = normalizeFixApplyResponse(payload);
        if (!result.ok) {
          clearPreview(id);
          showError(result.message || "Fix apply failed.");
          await loadQueue();
          return;
        }
        previews.delete(id);
        ignoreNextMutation = true;
        window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
        await loadQueue(result.message || "Fix applied.");
      } catch {
        clearPreview(id);
        showError("Fix apply failed.");
      } finally {
        setBusy(false);
      }
    };
    const runOpen2 = async (button) => {
      const key = button.getAttribute("data-fixes-open-key") || "";
      if (!key || !config.csrfToken) {
        showError("Open action unavailable.");
        return;
      }
      setBusy(true);
      setStatus("Opening editor...", "is-loading");
      try {
        const body = buildOpenNoteForm({ key, token: config.csrfToken });
        const request = jsonPostRequest2(config.openEndpoint, config.csrfToken, body.toString());
        const resp = await fetcher(request.url, request.init);
        const payload = await readResponsePayload2(resp);
        if (!resp.ok) {
          showError(normalizeOpenErrorMessage(resp.status, payload));
          return;
        }
        setError("");
        setStatus("Opened in editor. Refresh after saving changes.", "is-success");
      } catch {
        showError("Open failed.");
      } finally {
        setBusy(false);
      }
    };
    root.addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const fix = target.closest("[data-fixes-fix-action]");
      if (fix && root.contains(fix)) {
        void runPreview(fix);
        return;
      }
      const applyBtn = target.closest("[data-fixes-preview-apply]");
      if (applyBtn && root.contains(applyBtn)) {
        void runApply(applyBtn);
        return;
      }
      const cancelBtn = target.closest("[data-fixes-preview-cancel]");
      if (cancelBtn && root.contains(cancelBtn)) {
        clearPreview(cancelBtn.getAttribute("data-fixes-id") || "");
        return;
      }
      const itemBtn = target.closest("[data-fixes-item-action]");
      if (itemBtn && root.contains(itemBtn)) {
        void runItemAction(itemBtn);
        return;
      }
      const openBtn = target.closest("[data-fixes-open-key]");
      if (openBtn && root.contains(openBtn)) {
        void runOpen2(openBtn);
      }
    });
    window.addEventListener(managementMutationEventName, () => {
      if (ignoreNextMutation) {
        ignoreNextMutation = false;
        return;
      }
      void loadQueue("Fixes refreshed.");
    });
    void loadQueue();
  };

  // internal/web/static_src/editor-open.ts
  var readOpenEditorConfig = (container) => ({
    key: container.getAttribute("data-open-key") || "",
    csrfToken: container.getAttribute("data-open-csrf") || ""
  });
  var buildOpenEditorFetchRequest = (config) => {
    const body = buildOpenNoteForm({ key: config.key, token: config.csrfToken });
    return {
      url: "/api/open",
      init: {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-BN-CSRF": config.csrfToken
        },
        body: body.toString()
      }
    };
  };
  var initOpenInEditor = () => {
    const container = queryElement(document, "[data-open-editor]", HTMLElement);
    if (!container) {
      return;
    }
    const button = queryElement(container, "[data-open-button]", HTMLButtonElement);
    const statusEl = queryElement(container, "[data-open-status]", HTMLElement);
    if (!button || !statusEl) {
      return;
    }
    const config = readOpenEditorConfig(container);
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };
    button.addEventListener("click", async () => {
      if (!config.key || !config.csrfToken) {
        setStatus("Open action unavailable.", "is-error");
        return;
      }
      button.disabled = true;
      setStatus("Opening editor\u2026");
      try {
        const request = buildOpenEditorFetchRequest(config);
        const resp = await fetch(request.url, request.init);
        if (!resp.ok) {
          const detail = await resp.text();
          setStatus(normalizeOpenErrorMessage(resp.status, detail), "is-error");
          return;
        }
        setStatus("Opened in editor. Refresh after saving changes.", "is-success");
      } catch {
        setStatus("Open failed.", "is-error");
      } finally {
        button.disabled = false;
      }
    });
  };

  // internal/web/static_src/facets.ts
  var facetOptionKey = (option) => `${option.param}\0${option.value}`;
  var facetOptionMatchesQuery = (option, query) => {
    const normalizedQuery = query.trim().toLowerCase();
    if (normalizedQuery === "") {
      return true;
    }
    const haystack = `${option.groupLabel} ${option.label} ${option.value}`.toLowerCase();
    return haystack.includes(normalizedQuery);
  };
  var withFacetOptionSelected = (selection, option) => {
    const next = new Map(selection);
    if (!option.repeat) {
      for (const selectedOption of Array.from(next.values())) {
        if (selectedOption.param === option.param) {
          next.delete(facetOptionKey(selectedOption));
        }
      }
    }
    next.set(facetOptionKey(option), option);
    return next;
  };
  var facetSelectionFromParams = (params) => {
    let selection = /* @__PURE__ */ new Map();
    for (const param of sharedFilterParamKeys) {
      for (const rawValue of params.getAll(param)) {
        const value = normalizeParamValue(rawValue);
        if (value === "") {
          continue;
        }
        selection = withFacetOptionSelected(selection, fallbackFacetOption(param, value));
      }
    }
    return selection;
  };
  var hydrateFacetSelection = (selection, catalogOptions) => {
    const catalog = new Map(catalogOptions.map((option) => [facetOptionKey(option), option]));
    const next = /* @__PURE__ */ new Map();
    for (const option of selection.values()) {
      next.set(facetOptionKey(option), catalog.get(facetOptionKey(option)) || option);
    }
    return next;
  };
  var paramsForFacetSelection = (selection) => {
    const params = new URLSearchParams();
    for (const option of selection.values()) {
      params.append(option.param, option.value);
    }
    return params;
  };
  var initFacets = () => {
    const roots = queryAllElements(document, "[data-facet-controls]", HTMLElement);
    if (roots.length === 0) {
      return;
    }
    roots.forEach((root) => {
      const input = queryElement(root, ".facet-input", HTMLInputElement);
      const optionsEl = queryElement(root, ".facet-options", HTMLElement);
      const chipsEl = queryElement(root, "[data-facet-chips]", HTMLElement);
      const inputClear = queryElement(root, "[data-facet-input-clear]", HTMLButtonElement);
      const clearAll = queryElement(root, "[data-facet-clear-all]", HTMLButtonElement);
      if (!input || !optionsEl || !chipsEl) {
        return;
      }
      let allOptions = [];
      let filteredOptions = [];
      let activeIndex = -1;
      let loaded = false;
      let selected = facetSelectionFromParams(new URLSearchParams(window.location.search));
      const setExpanded = (expanded) => {
        input.setAttribute("aria-expanded", expanded ? "true" : "false");
        optionsEl.hidden = !expanded;
      };
      const setActive = (nextIndex) => {
        activeIndex = nextIndex;
        const options = optionsEl.querySelectorAll("[data-facet-index]");
        for (const option of options) {
          const index = Number(option.getAttribute("data-facet-index"));
          const active = index === activeIndex;
          option.classList.toggle("is-active", active);
          if (active) {
            input.setAttribute("aria-activedescendant", option.id);
          }
        }
        if (activeIndex < 0) {
          input.removeAttribute("aria-activedescendant");
        }
      };
      const currentFilterParams = () => paramsForFacetSelection(selected);
      const renderChips = () => {
        chipsEl.textContent = "";
        const options = Array.from(selected.values());
        chipsEl.hidden = options.length === 0;
        if (clearAll) {
          clearAll.hidden = options.length === 0;
        }
        for (const option of options) {
          const chip = document.createElement("span");
          chip.className = "facet-chip";
          const text = document.createElement("span");
          text.className = "facet-chip-text";
          text.textContent = `${option.groupLabel}: ${option.label}`;
          chip.appendChild(text);
          if (option.count > 0) {
            const count = document.createElement("span");
            count.className = "facet-chip-count";
            count.textContent = `(${option.count})`;
            chip.appendChild(count);
          }
          const remove = document.createElement("button");
          remove.type = "button";
          remove.className = "facet-chip-remove";
          remove.textContent = "x";
          remove.setAttribute("aria-label", `Remove ${option.groupLabel}: ${option.label}`);
          remove.addEventListener("click", () => {
            selected.delete(facetOptionKey(option));
            renderChips();
            renderOptions();
            emitChange();
            input.focus();
          });
          chip.appendChild(remove);
          chipsEl.appendChild(chip);
        }
      };
      const renderEmptyOption = (message) => {
        optionsEl.textContent = "";
        const item = document.createElement("li");
        item.className = "facet-option-empty";
        item.textContent = message;
        optionsEl.appendChild(item);
        setActive(-1);
      };
      const renderOptions = () => {
        if (inputClear) {
          inputClear.hidden = input.value.trim() === "";
        }
        if (!loaded) {
          renderEmptyOption("Loading facets...");
          return;
        }
        const query = input.value.trim().toLowerCase();
        filteredOptions = allOptions.filter((option) => facetOptionMatchesQuery(option, query)).slice(0, 80);
        optionsEl.textContent = "";
        if (filteredOptions.length === 0) {
          renderEmptyOption("No matching facets.");
          return;
        }
        filteredOptions.forEach((option, index) => {
          const item = document.createElement("li");
          const button = document.createElement("button");
          const selectedOption = selected.has(facetOptionKey(option));
          button.type = "button";
          button.id = `${input.id || "facet"}-option-${index}`;
          button.className = "facet-option";
          button.setAttribute("role", "option");
          button.setAttribute("aria-selected", selectedOption ? "true" : "false");
          button.setAttribute("data-facet-index", String(index));
          const main = document.createElement("span");
          main.className = "facet-option-main";
          const label = document.createElement("span");
          label.className = "facet-option-label";
          label.textContent = option.label;
          main.appendChild(label);
          const group = document.createElement("span");
          group.className = "facet-option-group";
          group.textContent = option.groupLabel;
          main.appendChild(group);
          button.appendChild(main);
          const count = document.createElement("span");
          count.className = "facet-option-count";
          count.textContent = String(option.count);
          button.appendChild(count);
          button.addEventListener("mouseenter", () => {
            setActive(index);
          });
          button.addEventListener("click", () => {
            toggleOption(option);
          });
          item.appendChild(button);
          optionsEl.appendChild(item);
        });
        setActive(
          filteredOptions.length > 0 ? Math.min(Math.max(activeIndex, -1), filteredOptions.length - 1) : -1
        );
      };
      const writeFiltersToURL = () => {
        const params = new URLSearchParams(window.location.search);
        for (const key of sharedFilterParamKeys) {
          params.delete(key);
        }
        for (const [key, value] of currentFilterParams()) {
          params.append(key, value);
        }
        updateBrowserQuery(params);
      };
      function emitChange() {
        writeFiltersToURL();
        dispatchFiltersChange(window, {
          mode: root.getAttribute("data-facet-mode") || "",
          params: currentFilterParams()
        });
      }
      function toggleOption(option) {
        const key = facetOptionKey(option);
        if (selected.has(key)) {
          selected.delete(key);
        } else {
          selected = withFacetOptionSelected(selected, option);
        }
        input.value = "";
        setExpanded(true);
        renderChips();
        renderOptions();
        emitChange();
      }
      input.addEventListener("focus", () => {
        setExpanded(true);
        renderOptions();
      });
      input.addEventListener("input", () => {
        setExpanded(true);
        setActive(-1);
        renderOptions();
      });
      input.addEventListener("keydown", (event) => {
        if (event.key === "ArrowDown") {
          event.preventDefault();
          setExpanded(true);
          const next = activeIndex < filteredOptions.length - 1 ? activeIndex + 1 : 0;
          setActive(filteredOptions.length > 0 ? next : -1);
          return;
        }
        if (event.key === "ArrowUp") {
          event.preventDefault();
          setExpanded(true);
          const next = activeIndex > 0 ? activeIndex - 1 : filteredOptions.length - 1;
          setActive(filteredOptions.length > 0 ? next : -1);
          return;
        }
        if (event.key === "Enter" && activeIndex >= 0 && activeIndex < filteredOptions.length) {
          event.preventDefault();
          toggleOption(filteredOptions[activeIndex]);
          return;
        }
        if (event.key === "Escape") {
          event.preventDefault();
          input.value = "";
          setExpanded(false);
          setActive(-1);
          renderOptions();
        }
      });
      if (inputClear) {
        inputClear.addEventListener("click", () => {
          input.value = "";
          setExpanded(true);
          renderOptions();
          input.focus();
        });
      }
      if (clearAll) {
        clearAll.addEventListener("click", () => {
          selected.clear();
          renderChips();
          renderOptions();
          emitChange();
          input.focus();
        });
      }
      document.addEventListener("click", (event) => {
        const target = event.target;
        if (target instanceof Node && !root.contains(target)) {
          setExpanded(false);
          setActive(-1);
        }
      });
      renderChips();
      renderOptions();
      fetch("/api/facets", {
        headers: { Accept: "application/json" }
      }).then((resp) => {
        if (!resp.ok) {
          throw new Error("facets request failed");
        }
        return resp.json();
      }).then((payload) => {
        allOptions = normalizeFacetOptions(payload);
        loaded = true;
        selected = hydrateFacetSelection(selected, allOptions);
        renderChips();
        renderOptions();
      }).catch(() => {
        loaded = true;
        allOptions = [];
        renderEmptyOption("Facets unavailable.");
      });
    });
  };

  // internal/web/static_src/frontmatter-edit.ts
  var readFrontmatterPanelConfig = (panel) => ({
    noteKey: panel.getAttribute("data-frontmatter-key") || "",
    csrfToken: panel.getAttribute("data-frontmatter-csrf") || ""
  });
  var readFrontmatterSetConfig = (form) => {
    const panel = form.closest("[data-frontmatter-panel]");
    const panelConfig = panel ? readFrontmatterPanelConfig(panel) : { noteKey: "", csrfToken: "" };
    const formData = new FormData(form);
    return {
      ...panelConfig,
      property: form.getAttribute("data-frontmatter-property") || String(formData.get("property") || "").trim(),
      op: normalizeFrontmatterOp(form.getAttribute("data-frontmatter-op")),
      value: String(formData.get("value") || "")
    };
  };
  var readFrontmatterUnsetConfig = (form) => {
    const panel = form.closest("[data-frontmatter-panel]");
    const panelConfig = panel ? readFrontmatterPanelConfig(panel) : { noteKey: "", csrfToken: "" };
    return {
      ...panelConfig,
      property: form.getAttribute("data-frontmatter-property") || ""
    };
  };
  var buildFrontmatterSetFetchRequest = (config) => frontmatterFetchRequest(
    "/api/notes/frontmatter/set",
    config.csrfToken,
    buildFrontmatterSetForm({
      key: config.noteKey,
      token: config.csrfToken,
      property: config.property,
      op: config.op,
      value: config.value
    })
  );
  var buildFrontmatterUnsetFetchRequest = (config) => frontmatterFetchRequest(
    "/api/notes/frontmatter/unset",
    config.csrfToken,
    buildFrontmatterUnsetForm({
      key: config.noteKey,
      token: config.csrfToken,
      property: config.property
    })
  );
  var initFrontmatterEditing = (deps = {}) => {
    const panel = queryElement(document, "[data-frontmatter-panel]", HTMLElement);
    if (!panel) {
      return;
    }
    const statusEl = queryElement(panel, "[data-frontmatter-status]", HTMLElement);
    const fetcher = deps.fetch || fetch;
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };
    for (const form of queryAllElements(panel, "[data-frontmatter-set]", HTMLFormElement)) {
      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        const config = readFrontmatterSetConfig(form);
        if (!validFrontmatterBaseConfig(config)) {
          setStatus("Frontmatter action unavailable.", "is-error");
          return;
        }
        setFormDisabled(form, true);
        setStatus("Updating frontmatter...");
        try {
          const request = buildFrontmatterSetFetchRequest(config);
          const resp = await fetcher(request.url, request.init);
          const payload = await readFrontmatterResponsePayload(resp);
          if (!resp.ok) {
            setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
            return;
          }
          const result = payload;
          setStatus(result && result.ok && result.message ? result.message : "Updated frontmatter.", "is-success");
        } catch {
          setStatus("Frontmatter update failed.", "is-error");
        } finally {
          setFormDisabled(form, false);
        }
      });
    }
    for (const form of queryAllElements(panel, "[data-frontmatter-unset]", HTMLFormElement)) {
      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        const config = readFrontmatterUnsetConfig(form);
        if (!validFrontmatterBaseConfig(config)) {
          setStatus("Frontmatter action unavailable.", "is-error");
          return;
        }
        setFormDisabled(form, true);
        setStatus("Updating frontmatter...");
        try {
          const request = buildFrontmatterUnsetFetchRequest(config);
          const resp = await fetcher(request.url, request.init);
          const payload = await readFrontmatterResponsePayload(resp);
          if (!resp.ok) {
            setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
            return;
          }
          const result = payload;
          setStatus(result && result.ok && result.message ? result.message : "Unset frontmatter.", "is-success");
        } catch {
          setStatus("Frontmatter update failed.", "is-error");
        } finally {
          setFormDisabled(form, false);
        }
      });
    }
  };
  var normalizeFrontmatterOp = (value) => {
    if (value === "+=" || value === "-=") {
      return value;
    }
    return "=";
  };
  var validFrontmatterBaseConfig = (config) => config.noteKey.trim() !== "" && config.csrfToken.trim() !== "" && config.property.trim() !== "";
  var frontmatterFetchRequest = (url, csrfToken, body) => ({
    url,
    init: {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": csrfToken
      },
      body: body.toString()
    }
  });
  var setFormDisabled = (form, disabled) => {
    for (const control of queryAllElements(form, "button, input", HTMLElement)) {
      if (control instanceof HTMLButtonElement || control instanceof HTMLInputElement) {
        control.disabled = disabled;
      }
    }
  };
  var readFrontmatterResponsePayload = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };

  // internal/web/static_src/graph.ts
  var asRecord2 = (value) => value && typeof value === "object" ? value : null;
  var readRawGraphNodeData = (value) => {
    const record = asRecord2(value);
    const data = record ? asRecord2(record.data) : null;
    return data || {};
  };
  var readRawGraphEdgeData = (value) => {
    const record = asRecord2(value);
    const data = record ? asRecord2(record.data) : null;
    return data || {};
  };
  var asRawGraphNodeArray = (value) => Array.isArray(value) ? value.map((item) => ({ data: readRawGraphNodeData(item) })) : [];
  var asRawGraphEdgeArray = (value) => Array.isArray(value) ? value.map((item) => ({ data: readRawGraphEdgeData(item) })) : [];
  var emptyGraphElements = () => ({ nodes: [], edges: [] });
  var readDegreeValue = (value, fallback) => {
    if (value === void 0 || value === null || value === "") {
      return fallback;
    }
    const parsed = Number(value);
    if (Number.isFinite(parsed) && parsed >= 0) {
      return Math.round(parsed);
    }
    return fallback;
  };
  var nodeSizeForDegree = (degree) => {
    const safeDegree = Number.isFinite(degree) && degree > 0 ? degree : 0;
    return Math.round(Math.min(40, 24 + Math.sqrt(safeDegree) * 4));
  };
  var hashFolder = (folder) => {
    let hash = 0;
    for (let i = 0; i < folder.length; i += 1) {
      hash = hash * 31 + folder.charCodeAt(i) >>> 0;
    }
    return hash;
  };
  var colorForFolder = (folder) => {
    if (!folder) {
      return "#7aa2ff";
    }
    const hue = hashFolder(folder) % 360;
    return `hsl(${hue}, 65%, 60%)`;
  };
  var DEFAULT_GRAPH_PALETTE = {
    nodeLabel: "#e5e9f0",
    labelOutline: "#0b0f16",
    labelBackground: "#0c1018",
    edge: "#66718a",
    neighbor: "#8cb8ff",
    hovered: "#f3f7ff",
    selected: "#ffd166",
    selectedText: "#fff3c2",
    broken: "#ff9d9d",
    orphan: "#c7d2e7"
  };
  var GRAPH_PALETTE_VARIABLES = {
    nodeLabel: "--bn-graph-node-label",
    labelOutline: "--bn-graph-label-outline",
    labelBackground: "--bn-graph-canvas-bg",
    edge: "--bn-graph-edge",
    neighbor: "--bn-graph-neighbor",
    hovered: "--bn-graph-hovered",
    selected: "--bn-graph-locked",
    selectedText: "--bn-graph-selected-text",
    broken: "--bn-graph-broken",
    orphan: "--bn-graph-orphan"
  };
  var readGraphPalette = (read, fallback = DEFAULT_GRAPH_PALETTE) => {
    const resolve = (key) => {
      let raw = "";
      try {
        raw = read(GRAPH_PALETTE_VARIABLES[key]);
      } catch {
        raw = "";
      }
      const value = typeof raw === "string" ? raw.trim() : "";
      return value || fallback[key];
    };
    return {
      nodeLabel: resolve("nodeLabel"),
      labelOutline: resolve("labelOutline"),
      labelBackground: resolve("labelBackground"),
      edge: resolve("edge"),
      neighbor: resolve("neighbor"),
      hovered: resolve("hovered"),
      selected: resolve("selected"),
      selectedText: resolve("selectedText"),
      broken: resolve("broken"),
      orphan: resolve("orphan")
    };
  };
  var buildGraphStyleRules = (palette = DEFAULT_GRAPH_PALETTE) => [
    {
      selector: "node",
      style: {
        label: "",
        "background-color": "data(color)",
        color: palette.nodeLabel,
        "font-size": "10px",
        "font-weight": 600,
        "text-valign": "bottom",
        "text-halign": "center",
        "text-margin-y": 6,
        "text-wrap": "wrap",
        "text-overflow-wrap": "anywhere",
        "text-max-width": 130,
        "text-outline-width": 1,
        "text-outline-color": palette.labelOutline,
        "text-background-color": palette.labelBackground,
        "text-background-opacity": 0.78,
        "text-background-padding": 2,
        "text-background-shape": "roundrectangle",
        width: "data(size)",
        height: "data(size)",
        "border-width": 0,
        opacity: 1,
        "z-index": 1
      }
    },
    {
      selector: "node.is-labeled",
      style: {
        label: "data(title)"
      }
    },
    {
      selector: "node.has-broken",
      style: {
        "border-width": 3,
        "border-color": palette.broken,
        shape: "diamond"
      }
    },
    {
      selector: "node.is-orphan",
      style: {
        "border-width": 2,
        "border-style": "dashed",
        "border-color": palette.orphan
      }
    },
    {
      selector: "node.is-dead-end",
      style: {
        "background-blacken": -0.12
      }
    },
    {
      selector: "node.is-neighbor",
      style: {
        "border-width": 2,
        "border-color": palette.neighbor,
        "z-index": 8
      }
    },
    {
      selector: "node.is-hovered",
      style: {
        "border-width": 3,
        "border-color": palette.hovered,
        "font-size": "11px",
        "z-index": 20
      }
    },
    {
      selector: "node.is-selected",
      style: {
        "border-width": 4,
        "border-color": palette.selected,
        color: palette.selectedText,
        "font-size": "11px",
        "z-index": 30
      }
    },
    {
      selector: "node.is-dimmed",
      style: {
        opacity: 0.22
      }
    },
    {
      selector: "edge",
      style: {
        width: 1.5,
        "line-color": palette.edge,
        "target-arrow-color": palette.edge,
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
        opacity: 0.72,
        "z-index": 0
      }
    },
    {
      selector: "edge.is-highlighted-edge",
      style: {
        width: 3,
        "line-color": palette.neighbor,
        "target-arrow-color": palette.neighbor,
        opacity: 1,
        "z-index": 12
      }
    },
    {
      selector: "edge.is-dimmed",
      style: {
        opacity: 0.12
      }
    }
  ];
  var computeFallbackGraphDegrees = (rawNodes, rawEdges) => {
    const degreeSets = /* @__PURE__ */ new Map();
    for (const node of rawNodes) {
      const id = normalizeParamValue(node.data.id);
      if (id === "") {
        continue;
      }
      degreeSets.set(id, {
        inSources: /* @__PURE__ */ new Set(),
        outTargets: /* @__PURE__ */ new Set()
      });
    }
    for (const edge of rawEdges) {
      const source = normalizeParamValue(edge.data.source);
      const target = normalizeParamValue(edge.data.target);
      if (source === "" || target === "" || source === target) {
        continue;
      }
      if (!degreeSets.has(source) || !degreeSets.has(target)) {
        continue;
      }
      degreeSets.get(source)?.outTargets.add(target);
      degreeSets.get(target)?.inSources.add(source);
    }
    const degrees = /* @__PURE__ */ new Map();
    for (const [id, sets] of degreeSets) {
      const inDegree = sets.inSources.size;
      const outDegree = sets.outTargets.size;
      degrees.set(id, {
        inDegree,
        outDegree,
        degree: inDegree + outDegree
      });
    }
    return degrees;
  };
  var normalizeGraphNodeData = (nodeData, fallback, fallbackID = "") => {
    const id = normalizeParamValue(nodeData.id || fallbackID);
    const folder = normalizeParamValue(nodeData.folder || "");
    const inDegree = readDegreeValue(nodeData.inDegree, fallback.inDegree);
    const outDegree = readDegreeValue(nodeData.outDegree, fallback.outDegree);
    const degree = readDegreeValue(nodeData.degree, inDegree + outDegree);
    const filteredInDegree = readDegreeValue(nodeData.filteredInDegree, inDegree);
    const filteredOutDegree = readDegreeValue(nodeData.filteredOutDegree, outDegree);
    const filteredDegree = readDegreeValue(
      nodeData.filteredDegree,
      filteredInDegree + filteredOutDegree
    );
    const brokenCount = readDegreeValue(nodeData.brokenCount, 0);
    return {
      id,
      title: normalizeParamValue(nodeData.title || id),
      folder,
      color: normalizeParamValue(nodeData.color) || colorForFolder(folder),
      inDegree,
      outDegree,
      degree,
      filteredInDegree,
      filteredOutDegree,
      filteredDegree,
      hasBroken: Boolean(nodeData.hasBroken) || brokenCount > 0,
      brokenCount,
      isOrphan: Boolean(nodeData.isOrphan),
      isDeadEnd: Boolean(nodeData.isDeadEnd),
      size: nodeSizeForDegree(filteredDegree)
    };
  };
  var emptyDegreeSummary = () => ({
    inDegree: 0,
    outDegree: 0,
    degree: 0
  });
  var graphNodeModelFromData = (value, fallbackID = "") => {
    const data = asRecord2(value);
    if (!data) {
      return fallbackID ? normalizeGraphNodeData({}, emptyDegreeSummary(), fallbackID) : null;
    }
    const inDegree = readDegreeValue(data.inDegree, 0);
    const outDegree = readDegreeValue(data.outDegree, 0);
    const degree = readDegreeValue(data.degree, inDegree + outDegree);
    return normalizeGraphNodeData(data, { inDegree, outDegree, degree }, fallbackID);
  };
  var normalizeGraphPayload = (payload) => {
    const data = asRecord2(payload) || {};
    const rawNodes = asRawGraphNodeArray(data.nodes);
    const rawEdges = asRawGraphEdgeArray(data.edges);
    const fallbackDegrees = computeFallbackGraphDegrees(rawNodes, rawEdges);
    const nodes = rawNodes.map((node) => {
      const id = normalizeParamValue(node.data.id);
      return normalizeGraphNodeData(node.data, fallbackDegrees.get(id) || emptyDegreeSummary());
    });
    const edges = rawEdges.map((edge) => ({
      id: normalizeParamValue(edge.data.id),
      source: normalizeParamValue(edge.data.source),
      target: normalizeParamValue(edge.data.target)
    }));
    return {
      nodes,
      edges
    };
  };
  var graphNodeClasses = (node) => {
    const classes = [];
    if (node.hasBroken) {
      classes.push("has-broken");
    }
    if (node.isOrphan) {
      classes.push("is-orphan");
    }
    if (node.isDeadEnd) {
      classes.push("is-dead-end");
    }
    return classes.join(" ");
  };
  var toCytoscapeElements = (graphElements) => [
    ...graphElements.nodes.map((node) => ({
      classes: graphNodeClasses(node),
      data: {
        id: node.id,
        title: node.title,
        folder: node.folder,
        color: node.color,
        inDegree: node.inDegree,
        outDegree: node.outDegree,
        degree: node.degree,
        filteredInDegree: node.filteredInDegree,
        filteredOutDegree: node.filteredOutDegree,
        filteredDegree: node.filteredDegree,
        hasBroken: node.hasBroken,
        brokenCount: node.brokenCount,
        isOrphan: node.isOrphan,
        isDeadEnd: node.isDeadEnd,
        size: node.size
      }
    })),
    ...graphElements.edges.map((edge) => ({
      data: {
        id: edge.id,
        source: edge.source,
        target: edge.target
      }
    }))
  ];
  var graphScopeLabel = (query, hasFilters) => {
    if (query && hasFilters) {
      return `"${query}" with the active filters`;
    }
    if (query) {
      return `"${query}"`;
    }
    if (hasFilters) {
      return "the active filters";
    }
    return "";
  };

  // internal/web/static_src/graph-controller-details.ts
  var formatDegreeDetails = (data) => {
    if (!data) {
      return "";
    }
    const inDegree = readDegreeValue(data.inDegree, 0);
    const outDegree = readDegreeValue(data.outDegree, 0);
    const degree = readDegreeValue(data.degree, inDegree + outDegree);
    const filteredInDegree = readDegreeValue(data.filteredInDegree, inDegree);
    const filteredOutDegree = readDegreeValue(data.filteredOutDegree, outDegree);
    const filteredDegree = readDegreeValue(data.filteredDegree, filteredInDegree + filteredOutDegree);
    const visibleDetail = `Visible degree: ${filteredDegree} (${filteredInDegree} in, ${filteredOutDegree} out)`;
    if (filteredDegree === degree && filteredInDegree === inDegree && filteredOutDegree === outDegree) {
      return visibleDetail;
    }
    return `${visibleDetail}. Full graph: ${degree} (${inDegree} in, ${outDegree} out)`;
  };
  var issueItemsForNode = (data) => {
    if (!data) {
      return [];
    }
    const items = [];
    const brokenCount = readDegreeValue(data.brokenCount, 0);
    if (data.hasBroken || brokenCount > 0) {
      items.push({
        className: "is-broken",
        label: brokenCount > 0 ? brokenCount === 1 ? "1 broken link" : `${brokenCount} broken links` : "Broken links"
      });
    }
    if (data.isOrphan) {
      items.push({
        className: "is-orphan",
        label: "Orphan"
      });
    }
    if (data.isDeadEnd) {
      items.push({
        className: "is-dead-end",
        label: "Dead end"
      });
    }
    return items;
  };
  var renderIssueDetails = (hoverIssues, model) => {
    if (!hoverIssues) {
      return;
    }
    hoverIssues.textContent = "";
    const items = issueItemsForNode(model);
    hoverIssues.hidden = items.length === 0;
    for (const item of items) {
      const issue = document.createElement("li");
      issue.className = `graph-issue-item ${item.className}`;
      issue.textContent = item.label;
      hoverIssues.appendChild(issue);
    }
  };
  var setOpenNoteTarget = (openNote, nodeID) => {
    if (!openNote) {
      return;
    }
    const path = notePathForKey(nodeID);
    if (!path) {
      openNote.removeAttribute("href");
      openNote.classList.add("is-disabled");
      openNote.setAttribute("aria-disabled", "true");
      openNote.setAttribute("tabindex", "-1");
      return;
    }
    openNote.setAttribute("href", path);
    openNote.classList.remove("is-disabled");
    openNote.setAttribute("aria-disabled", "false");
    openNote.setAttribute("tabindex", "0");
  };
  var renderGraphDetails = (refs, selectedModel, hoveredModel) => {
    const model = selectedModel || hoveredModel;
    if (refs.hoverTitle) {
      refs.hoverTitle.textContent = model ? model.title || model.id : "Hover a node";
    }
    if (refs.hoverFolder) {
      refs.hoverFolder.textContent = model ? `Folder: ${model.folder || "/"}` : "";
    }
    if (refs.hoverDegree) {
      refs.hoverDegree.textContent = model ? formatDegreeDetails(model) : "";
    }
    renderIssueDetails(refs.hoverIssues, model);
    if (refs.detailsPanel) {
      refs.detailsPanel.classList.toggle("is-locked", Boolean(selectedModel));
      refs.detailsPanel.classList.toggle("is-transient", !selectedModel && Boolean(hoveredModel));
    }
    setOpenNoteTarget(refs.openNote, selectedModel ? selectedModel.id : "");
  };

  // internal/web/static_src/graph-controller-labels.ts
  var labelPolicy = {
    smallGraphNodeLimit: 24,
    selectedNeighborLabelLimit: 10,
    zoomLabelMinZoom: 1.05,
    zoomVisibleLabelLimit: 22,
    zoomPriorityLabelLimit: 10,
    deepZoomMinZoom: 1.6,
    deepZoomPriorityLabelLimit: 24
  };
  var readLabelMode = (inputs) => {
    for (const input of inputs) {
      if (input.checked) {
        return input.value === "all" ? "all" : "auto";
      }
    }
    return "auto";
  };
  var cyNodeByID = (cy, nodeID) => {
    if (!cy || !nodeID) {
      return null;
    }
    const node = cy.getElementById(nodeID);
    if (!node || node.length === 0 || !node.isNode()) {
      return null;
    }
    return node;
  };
  var nodeDegree = (node) => {
    if (!node) {
      return 0;
    }
    const filteredDegree = Number(node.data("filteredDegree"));
    if (Number.isFinite(filteredDegree) && filteredDegree > 0) {
      return filteredDegree;
    }
    const degree = Number(node.data("degree"));
    return Number.isFinite(degree) && degree > 0 ? degree : 0;
  };
  var sortedNodesByImportance = (nodes) => nodes.slice().sort((left, right) => nodeDegree(right) - nodeDegree(left) || left.id().localeCompare(right.id()));
  var connectedNodeIDs = (node) => {
    const ids = /* @__PURE__ */ new Set();
    if (!node) {
      return ids;
    }
    node.connectedEdges().forEach((edge) => {
      edge.connectedNodes().forEach((connectedNode) => {
        const id = connectedNode.id();
        if (id !== node.id()) {
          ids.add(id);
        }
      });
    });
    return ids;
  };
  var visibleNodesForViewport = (cy) => {
    if (!cy) {
      return [];
    }
    const zoom = cy.zoom();
    const padding = 72 / Math.max(zoom, 0.1);
    const extent = cy.extent();
    return cy.nodes().filter((node) => {
      const position = node.position();
      return position.x >= extent.x1 - padding && position.x <= extent.x2 + padding && position.y >= extent.y1 - padding && position.y <= extent.y2 + padding;
    }).toArray();
  };
  var addZoomLabels = (cy, labelIDs) => {
    if (!cy) {
      return;
    }
    const zoom = cy.zoom();
    if (zoom < labelPolicy.zoomLabelMinZoom) {
      return;
    }
    const visibleNodes = visibleNodesForViewport(cy);
    if (visibleNodes.length <= labelPolicy.zoomVisibleLabelLimit) {
      for (const node of visibleNodes) {
        labelIDs.add(node.id());
      }
      return;
    }
    const priorityLimit = zoom >= labelPolicy.deepZoomMinZoom ? labelPolicy.deepZoomPriorityLabelLimit : labelPolicy.zoomPriorityLabelLimit;
    for (const node of sortedNodesByImportance(visibleNodes).slice(0, priorityLimit)) {
      labelIDs.add(node.id());
    }
  };
  var labelIDsForCurrentState = (cy, labelMode, selectedNodeID, hoveredNodeID) => {
    const labelIDs = /* @__PURE__ */ new Set();
    if (!cy) {
      return labelIDs;
    }
    if (labelMode === "all" || cy.nodes().length <= labelPolicy.smallGraphNodeLimit) {
      cy.nodes().forEach((node) => {
        labelIDs.add(node.id());
      });
    } else {
      addZoomLabels(cy, labelIDs);
    }
    const selectedNode = cyNodeByID(cy, selectedNodeID);
    const hoveredNode = cyNodeByID(cy, hoveredNodeID);
    if (selectedNode) {
      labelIDs.add(selectedNode.id());
      const selectedNeighborIDs = connectedNodeIDs(selectedNode);
      if (selectedNeighborIDs.size <= labelPolicy.selectedNeighborLabelLimit) {
        for (const id of selectedNeighborIDs) {
          labelIDs.add(id);
        }
      }
    }
    if (hoveredNode) {
      labelIDs.add(hoveredNode.id());
    }
    return labelIDs;
  };
  var visualFocusState = (cy, selectedNodeID, hoveredNodeID) => {
    const selectedNode = cyNodeByID(cy, selectedNodeID);
    const hoveredNode = cyNodeByID(cy, hoveredNodeID);
    const focusNode = selectedNode || hoveredNode;
    const neighborIDs = connectedNodeIDs(focusNode);
    const highlightedEdgeIDs = /* @__PURE__ */ new Set();
    if (focusNode) {
      focusNode.connectedEdges().forEach((edge) => {
        highlightedEdgeIDs.add(edge.id());
      });
    }
    return {
      focusNodeID: focusNode ? focusNode.id() : "",
      selectedID: selectedNode ? selectedNode.id() : "",
      hoveredID: hoveredNode ? hoveredNode.id() : "",
      neighborIDs,
      highlightedEdgeIDs
    };
  };
  var applyGraphVisualClasses = (cy, labelMode, selectedNodeID, hoveredNodeID) => {
    if (!cy) {
      return;
    }
    const labelIDs = labelIDsForCurrentState(cy, labelMode, selectedNodeID, hoveredNodeID);
    const focus = visualFocusState(cy, selectedNodeID, hoveredNodeID);
    cy.batch(() => {
      cy.nodes().forEach((node) => {
        const id = node.id();
        const isSelected = id === focus.selectedID;
        const isHovered = id === focus.hoveredID;
        const isNeighbor = focus.neighborIDs.has(id);
        const isFocusedNode = focus.focusNodeID !== "" && id === focus.focusNodeID;
        const isDimmed = focus.focusNodeID !== "" && !isFocusedNode && !isHovered && !isNeighbor;
        node.toggleClass("is-labeled", labelIDs.has(id));
        node.toggleClass("is-selected", isSelected);
        node.toggleClass("is-hovered", isHovered);
        node.toggleClass("is-neighbor", isNeighbor);
        node.toggleClass("is-dimmed", isDimmed);
      });
      cy.edges().forEach((edge) => {
        const isHighlighted = focus.highlightedEdgeIDs.has(edge.id());
        edge.toggleClass("is-highlighted-edge", isHighlighted);
        edge.toggleClass("is-dimmed", focus.focusNodeID !== "" && !isHighlighted);
      });
    });
  };

  // internal/web/static_src/graph-controller-layout.ts
  var readSpread = (input) => {
    if (!input) {
      return 2;
    }
    const parsed = Number.parseFloat(input.value);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      return 2;
    }
    return parsed;
  };
  var clampedSpread = (input) => Math.min(6, Math.max(0.8, readSpread(input)));
  var updateSpreadLabel = (input, spreadValue) => {
    if (!spreadValue) {
      return;
    }
    const spread = readSpread(input);
    spreadValue.textContent = `${spread.toFixed(1)}x`;
  };
  var layoutOptionsForSpread = (spread) => ({
    name: "cose",
    animate: false,
    fit: false,
    randomize: false,
    padding: 72,
    idealEdgeLength: Math.round(70 + 55 * spread),
    nodeRepulsion: Math.round(7e3 * spread * spread),
    componentSpacing: Math.round(100 + 85 * spread),
    nodeOverlap: Math.round(12 + 16 * spread),
    gravity: Math.max(0.05, 0.62 - 0.09 * spread),
    numIter: Math.round(900 + 120 * spread),
    initialTemp: Math.round(180 + 45 * spread),
    coolingFactor: 0.95,
    minTemp: 1
  });
  var seedDeterministicPositions = (cy, spread) => {
    if (!cy) {
      return;
    }
    const nodes = cy.nodes().toArray().sort((left, right) => left.id().localeCompare(right.id()));
    const radialGap = 52 + 26 * spread;
    const goldenAngle = Math.PI * (3 - Math.sqrt(5));
    nodes.forEach((node, index) => {
      const radius = radialGap * Math.sqrt(index);
      const angle = index * goldenAngle;
      node.position({
        x: Math.cos(angle) * radius,
        y: Math.sin(angle) * radius
      });
    });
  };
  var separateIsolatedNodes = (cy, spread) => {
    if (!cy) {
      return;
    }
    const isolatedNodes = cy.nodes().filter((node) => node.connectedEdges().length === 0).toArray().sort((left, right) => left.id().localeCompare(right.id()));
    if (isolatedNodes.length === 0) {
      return;
    }
    const spacing = 58 + 34 * spread;
    const columns = Math.ceil(Math.sqrt(isolatedNodes.length));
    const rows = Math.ceil(isolatedNodes.length / columns);
    const connectedNodes = cy.nodes().filter((node) => node.connectedEdges().length > 0);
    let startX = -((columns - 1) * spacing) / 2;
    let startY = -((rows - 1) * spacing) / 2;
    if (connectedNodes.length > 0) {
      const box = connectedNodes.boundingBox();
      startX = box.x2 + spacing * 1.6;
      startY = box.y1 + (box.h - (rows - 1) * spacing) / 2;
    }
    isolatedNodes.forEach((node, index) => {
      const column = index % columns;
      const row = Math.floor(index / columns);
      node.position({
        x: startX + column * spacing,
        y: startY + row * spacing
      });
    });
  };
  var currentViewport = (cy) => {
    if (!cy) {
      return null;
    }
    const pan = cy.pan();
    return {
      pan: {
        x: pan.x,
        y: pan.y
      },
      zoom: cy.zoom()
    };
  };
  var applyViewport = (cy, viewport) => {
    if (!cy || !viewport) {
      return;
    }
    cy.viewport({
      pan: {
        x: viewport.pan.x,
        y: viewport.pan.y
      },
      zoom: viewport.zoom
    });
  };

  // internal/web/static_src/graph-controller-style.ts
  var currentGraphPalette = () => {
    if (typeof window === "undefined" || typeof document === "undefined") {
      return DEFAULT_GRAPH_PALETTE;
    }
    const root = document.documentElement;
    if (!root || typeof window.getComputedStyle !== "function") {
      return DEFAULT_GRAPH_PALETTE;
    }
    const styles = window.getComputedStyle(root);
    return readGraphPalette((name) => styles.getPropertyValue(name));
  };
  var buildGraphStyle = () => buildGraphStyleRules(currentGraphPalette());

  // internal/web/static_src/theme.ts
  var THEME_STORAGE_KEY = "bn.theme";
  var THEME_EVENT = "bn:themechange";
  var DARK_MEDIA_QUERY = "(prefers-color-scheme: dark)";
  var isExplicitPreference = (value) => value === "light" || value === "dark";
  var readStoredPreference = (storage) => {
    if (!storage) {
      return "system";
    }
    let stored;
    try {
      stored = storage.getItem(THEME_STORAGE_KEY);
    } catch {
      return "system";
    }
    return isExplicitPreference(stored) ? stored : "system";
  };
  var resolveEffectiveTheme = (preference, prefersDark) => {
    if (isExplicitPreference(preference)) {
      return preference;
    }
    return prefersDark ? "dark" : "light";
  };
  var applyThemePreference = (root, preference) => {
    if (isExplicitPreference(preference)) {
      root.dataset.theme = preference;
    } else {
      delete root.dataset.theme;
    }
  };
  var persistThemePreference = (storage, preference) => {
    if (!storage) {
      return;
    }
    try {
      if (isExplicitPreference(preference)) {
        storage.setItem(THEME_STORAGE_KEY, preference);
      } else {
        storage.removeItem(THEME_STORAGE_KEY);
      }
    } catch {
    }
  };
  var safeLocalStorage = (view) => {
    if (!view) {
      return null;
    }
    try {
      return view.localStorage;
    } catch {
      return null;
    }
  };
  var initTheme = (options = {}) => {
    const doc = options.document ?? (typeof document !== "undefined" ? document : null);
    if (!doc) {
      return;
    }
    const root = doc.documentElement;
    if (!root) {
      return;
    }
    const view = doc.defaultView;
    const storage = options.storage !== void 0 ? options.storage : safeLocalStorage(view);
    const darkQuery = view && typeof view.matchMedia === "function" ? view.matchMedia(DARK_MEDIA_QUERY) : null;
    const prefersDark = () => Boolean(darkQuery && darkQuery.matches);
    const controls = queryAllElements(doc, "[data-theme-control]", HTMLElement);
    const syncControls = (preference) => {
      controls.forEach((control) => {
        queryAllElements(control, "[data-theme-option]", HTMLInputElement).forEach(
          (option) => {
            option.checked = option.value === preference;
          }
        );
      });
    };
    const apply = (preference) => {
      applyThemePreference(root, preference);
      syncControls(preference);
      const detail = {
        preference,
        effective: resolveEffectiveTheme(preference, prefersDark())
      };
      doc.dispatchEvent(new CustomEvent(THEME_EVENT, { detail }));
    };
    apply(readStoredPreference(storage));
    controls.forEach((control) => {
      control.addEventListener("change", (event) => {
        const target = event.target;
        if (!(target instanceof HTMLInputElement) || !target.hasAttribute("data-theme-option")) {
          return;
        }
        const preference = isExplicitPreference(target.value) ? target.value : "system";
        persistThemePreference(storage, preference);
        apply(preference);
      });
    });
    if (view) {
      view.addEventListener("storage", (event) => {
        if (event.key !== null && event.key !== THEME_STORAGE_KEY) {
          return;
        }
        apply(readStoredPreference(storage));
      });
    }
    if (darkQuery) {
      const onSystemChange = () => {
        if (readStoredPreference(storage) === "system") {
          apply("system");
        }
      };
      if (typeof darkQuery.addEventListener === "function") {
        darkQuery.addEventListener("change", onSystemChange);
      } else if (typeof darkQuery.addListener === "function") {
        darkQuery.addListener(onSystemChange);
      }
    }
  };

  // internal/web/static_src/graph-controller.ts
  var initGraph = () => {
    const dom = {
      canvas: getElementByID("graph-canvas", HTMLElement),
      status: getElementByID("graph-status", HTMLElement),
      detailsPanel: queryElement(document, ".graph-hover", HTMLElement),
      hoverTitle: getElementByID("graph-hover-title", HTMLElement),
      hoverFolder: getElementByID("graph-hover-folder", HTMLElement),
      hoverDegree: getElementByID("graph-hover-degree", HTMLElement),
      hoverIssues: getElementByID("graph-hover-issues", HTMLElement),
      openNote: getElementByID("graph-open-note", HTMLAnchorElement),
      filterInput: getElementByID("graph-filter-input", HTMLInputElement),
      spreadInput: getElementByID("graph-spread-input", HTMLInputElement),
      spreadValue: getElementByID("graph-spread-value", HTMLElement),
      fitButton: getElementByID("graph-fit-button", HTMLButtonElement),
      resetLayoutButton: getElementByID("graph-reset-layout-button", HTMLButtonElement),
      labelModeInputs: queryAllElements(document, 'input[name="graph-label-mode"]', HTMLInputElement)
    };
    if (!dom.canvas) {
      return;
    }
    const canvas = dom.canvas;
    const detailsRefs = {
      detailsPanel: dom.detailsPanel,
      hoverTitle: dom.hoverTitle,
      hoverFolder: dom.hoverFolder,
      hoverDegree: dom.hoverDegree,
      hoverIssues: dom.hoverIssues,
      openNote: dom.openNote
    };
    const state = {
      cy: null,
      activeLayout: null,
      currentQuery: "",
      currentGraphElements: emptyGraphElements(),
      hoveredNodeID: "",
      selectedNodeID: "",
      labelMode: readLabelMode(dom.labelModeInputs),
      lastMeaningfulViewport: null,
      layoutRunID: 0
    };
    let debounceToken = 0;
    let requestToken = 0;
    let selectionSyncing = false;
    function setStatus(message, kind = "") {
      setStatusText(dom.status, message, kind, ["is-loading", "is-empty", "is-error"]);
    }
    function renderDetailsPanel(fallbackHoveredModel = null) {
      const selectedModel = findNodeModel(state.selectedNodeID);
      const hoveredModel = selectedModel ? null : findNodeModel(state.hoveredNodeID) || fallbackHoveredModel || null;
      renderGraphDetails(detailsRefs, selectedModel, hoveredModel);
    }
    function setLabelMode(nextMode) {
      state.labelMode = nextMode === "all" ? "all" : "auto";
      applyGraphVisualState();
    }
    function restyleGraphForTheme() {
      if (!state.cy) {
        return;
      }
      state.cy.style(buildGraphStyle());
      applyGraphVisualState();
    }
    function stopActiveLayout() {
      if (state.activeLayout && typeof state.activeLayout.stop === "function") {
        state.activeLayout.stop();
      }
      state.activeLayout = null;
    }
    function fitCurrentGraph() {
      if (!state.cy || state.cy.nodes().length === 0) {
        return;
      }
      state.cy.fit(state.cy.elements(), 72);
      rememberMeaningfulViewport();
    }
    function rememberMeaningfulViewport() {
      if (!state.cy || state.cy.nodes().length === 0) {
        return;
      }
      state.lastMeaningfulViewport = currentViewport(state.cy);
    }
    function ensureGraph() {
      if (state.cy) {
        return state.cy;
      }
      const createCytoscape = window.cytoscape;
      state.cy = createCytoscape({
        container: canvas,
        elements: [],
        style: buildGraphStyle()
      });
      bindGraphEvents(state.cy);
      return state.cy;
    }
    function bindGraphEvents(instance) {
      instance.on("tap", "node", (event) => {
        selectNode(event.target.id());
      });
      instance.on("tap", (event) => {
        if (event.target === instance) {
          clearSelectedNode();
        }
      });
      instance.on("mouseover", "node", (event) => {
        const data = event.target.data();
        canvas.classList.add("is-node-hovered");
        setHoveredNode(event.target.id(), data);
      });
      instance.on("mouseout", "node", () => {
        canvas.classList.remove("is-node-hovered");
        setHoveredNode("", null);
      });
      instance.on("select", "node", (event) => {
        if (!selectionSyncing) {
          selectNode(event.target.id(), { fromCytoscape: true });
        }
      });
      instance.on("unselect", "node", (event) => {
        if (!selectionSyncing && event.target.id() === state.selectedNodeID) {
          clearSelectedNode({ fromCytoscape: true });
        }
      });
      instance.on("viewport", () => {
        rememberMeaningfulViewport();
        applyGraphVisualState();
      });
    }
    function findNodeModel(nodeID) {
      return state.currentGraphElements.nodes.find((node) => node.id === nodeID) || null;
    }
    function syncCytoscapeSelection(nodeID) {
      if (!state.cy || selectionSyncing) {
        return;
      }
      selectionSyncing = true;
      try {
        state.cy.nodes().forEach((node) => {
          const shouldSelect = node.id() === nodeID;
          if (shouldSelect && !node.selected()) {
            node.select();
          } else if (!shouldSelect && node.selected()) {
            node.unselect();
          }
        });
      } finally {
        selectionSyncing = false;
      }
    }
    function selectNode(nodeID, options = {}) {
      const model = findNodeModel(nodeID);
      state.selectedNodeID = model ? model.id : "";
      if (!options || !options.fromCytoscape) {
        syncCytoscapeSelection(state.selectedNodeID);
      }
      renderDetailsPanel();
      applyGraphVisualState();
    }
    function clearSelectedNode(options = {}) {
      state.selectedNodeID = "";
      if (!options || !options.fromCytoscape) {
        syncCytoscapeSelection("");
      }
      renderDetailsPanel();
      applyGraphVisualState();
    }
    function setHoveredNode(nodeID, fallbackData) {
      state.hoveredNodeID = nodeID || "";
      if (!state.hoveredNodeID) {
        renderDetailsPanel();
        applyGraphVisualState();
        return;
      }
      const fallbackModel = fallbackData ? graphNodeModelFromData(fallbackData, state.hoveredNodeID) : null;
      renderDetailsPanel(fallbackModel);
      applyGraphVisualState();
    }
    function keyboardTargetNodeID() {
      if (state.hoveredNodeID && findNodeModel(state.hoveredNodeID)) {
        return state.hoveredNodeID;
      }
      if (state.selectedNodeID && findNodeModel(state.selectedNodeID)) {
        return state.selectedNodeID;
      }
      if (!state.cy) {
        return "";
      }
      const selectedNode = state.cy.nodes().filter((node) => node.selected()).first();
      return selectedNode && selectedNode.length > 0 ? selectedNode.id() : "";
    }
    function handleGraphKeyDown(event) {
      if (event.key !== "Enter" && event.key !== " " && event.key !== "Spacebar") {
        return;
      }
      const nodeID = keyboardTargetNodeID();
      if (!nodeID) {
        return;
      }
      event.preventDefault();
      selectNode(nodeID);
    }
    function applyGraphVisualState() {
      applyGraphVisualClasses(state.cy, state.labelMode, state.selectedNodeID, state.hoveredNodeID);
    }
    function runLayout(options = {}) {
      if (!state.cy || state.cy.nodes().length === 0) {
        return;
      }
      const layoutOptions = options;
      const fitAfterLayout = Boolean(layoutOptions.fitAfterLayout);
      const preservedViewport = layoutOptions.preserveViewport ? currentViewport(state.cy) : null;
      const runID = state.layoutRunID + 1;
      state.layoutRunID = runID;
      stopActiveLayout();
      if (layoutOptions.resetPositions) {
        seedDeterministicPositions(state.cy, clampedSpread(dom.spreadInput));
      }
      let finalized = false;
      const finalizeLayout = () => {
        if (finalized || runID !== state.layoutRunID) {
          return;
        }
        finalized = true;
        separateIsolatedNodes(state.cy, clampedSpread(dom.spreadInput));
        if (fitAfterLayout) {
          fitCurrentGraph();
        } else if (preservedViewport) {
          applyViewport(state.cy, preservedViewport);
          rememberMeaningfulViewport();
        } else {
          rememberMeaningfulViewport();
        }
        applyGraphVisualState();
        state.activeLayout = null;
      };
      const layout = state.cy.layout(layoutOptionsForSpread(clampedSpread(dom.spreadInput)));
      state.activeLayout = layout;
      if (typeof layout.one === "function") {
        layout.one("layoutstop", finalizeLayout);
      }
      layout.run();
      if (typeof layout.one !== "function") {
        finalizeLayout();
      }
    }
    function applyGraphElements(graphElements, fitAfterLayout) {
      const instance = ensureGraph();
      stopActiveLayout();
      const selectedNodeID = state.selectedNodeID;
      selectionSyncing = true;
      try {
        instance.elements().remove();
        if (graphElements.nodes.length > 0 || graphElements.edges.length > 0) {
          instance.add(toCytoscapeElements(graphElements));
        }
      } finally {
        selectionSyncing = false;
      }
      state.selectedNodeID = graphElements.nodes.some((node) => node.id === selectedNodeID) ? selectedNodeID : "";
      syncCytoscapeSelection(state.selectedNodeID);
      setHoveredNode("", null);
      if (instance.nodes().length === 0) {
        state.lastMeaningfulViewport = null;
        applyGraphVisualState();
        return;
      }
      runLayout({
        fitAfterLayout,
        resetPositions: true
      });
    }
    function buildQueryURL(query) {
      return buildFilteredAPIURL("/api/graph.json", query);
    }
    async function loadGraph(query, localToken) {
      if (typeof window.cytoscape !== "function") {
        setStatus("Graph library failed to load.", "is-error");
        return;
      }
      setStatus("Loading graph...", "is-loading");
      let payload;
      try {
        const resp = await fetch(buildQueryURL(query), {
          headers: { Accept: "application/json" }
        });
        if (localToken !== requestToken) {
          return;
        }
        if (!resp.ok) {
          setStatus(`Graph load failed (${resp.status}).`, "is-error");
          return;
        }
        payload = await resp.json();
      } catch (error) {
        if (localToken !== requestToken) {
          return;
        }
        setStatus("Graph load failed.", "is-error");
        return;
      }
      if (localToken !== requestToken) {
        return;
      }
      state.currentGraphElements = normalizeGraphPayload(payload);
      applyGraphElements(state.currentGraphElements, true);
      const hasFilters = hasActiveSharedFilters();
      const scopeLabel = graphScopeLabel(query, hasFilters);
      if (scopeLabel) {
        if (state.currentGraphElements.nodes.length === 0) {
          setStatus(`No notes match ${scopeLabel}.`, "is-empty");
          return;
        }
        setStatus(
          `Showing ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges for ${scopeLabel}.`
        );
        return;
      }
      setStatus(
        `Loaded ${state.currentGraphElements.nodes.length} nodes and ${state.currentGraphElements.edges.length} edges.`
      );
    }
    function scheduleLoad(query, delay) {
      window.clearTimeout(debounceToken);
      state.currentQuery = query;
      const localToken = ++requestToken;
      if (delay <= 0) {
        void loadGraph(query, localToken);
        return;
      }
      debounceToken = window.setTimeout(() => {
        void loadGraph(query, localToken);
      }, delay);
    }
    function bindControls() {
      canvas.addEventListener("keydown", handleGraphKeyDown);
      const openNote = dom.openNote;
      if (openNote instanceof HTMLAnchorElement) {
        openNote.addEventListener("click", (event) => {
          if (openNote.getAttribute("aria-disabled") === "true") {
            event.preventDefault();
          }
        });
      }
      const filterInput2 = dom.filterInput;
      if (filterInput2 instanceof HTMLInputElement) {
        filterInput2.addEventListener("input", () => {
          const query = filterInput2.value.trim();
          syncPageQueryParam(query);
          scheduleLoad(query, 150);
        });
      }
      updateSpreadLabel(dom.spreadInput, dom.spreadValue);
      if (dom.spreadInput instanceof HTMLInputElement) {
        dom.spreadInput.addEventListener("input", () => {
          updateSpreadLabel(dom.spreadInput, dom.spreadValue);
          runLayout({
            fitAfterLayout: false,
            preserveViewport: true
          });
        });
      }
      if (dom.fitButton instanceof HTMLButtonElement) {
        dom.fitButton.addEventListener("click", fitCurrentGraph);
      }
      if (dom.resetLayoutButton instanceof HTMLButtonElement) {
        dom.resetLayoutButton.addEventListener("click", () => {
          runLayout({
            fitAfterLayout: true,
            resetPositions: true
          });
        });
      }
      for (const input of dom.labelModeInputs) {
        if (!(input instanceof HTMLInputElement)) {
          continue;
        }
        input.addEventListener("change", () => {
          if (input.checked) {
            setLabelMode(input.value);
          }
        });
      }
    }
    const initialQuery = readPageQuery();
    const filterInput = dom.filterInput;
    if (filterInput instanceof HTMLInputElement) {
      filterInput.value = initialQuery;
    }
    window.addEventListener(filterChangeEventName, () => {
      const query = filterInput instanceof HTMLInputElement ? filterInput.value.trim() : state.currentQuery;
      scheduleLoad(query, 0);
    });
    document.addEventListener(THEME_EVENT, restyleGraphForTheme);
    bindControls();
    scheduleLoad(initialQuery, 0);
  };

  // internal/web/static_src/history-panel.ts
  var formatHistoryTimestamp = (value) => {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return value;
    }
    return date.toLocaleString(void 0, {
      dateStyle: "short",
      timeStyle: "short"
    });
  };
  var historyFileCountLabel = (count) => count === 1 ? "1 path" : `${count} paths`;
  var historyConflictSummary = (conflicts) => {
    if (conflicts.length === 0) {
      return "Undo has conflicts.";
    }
    const sample = conflicts.slice(0, 3).map((conflict) => {
      const path = conflict.path || "path";
      const expected = conflict.expected_state || "expected";
      const current = conflict.current_state || "current";
      return `${path}: expected ${expected}, current ${current}`;
    });
    const suffix = conflicts.length > sample.length ? `; ${conflicts.length - sample.length} more` : "";
    return `Undo has conflicts: ${sample.join("; ")}${suffix}.`;
  };
  var initHistoryPanel = (deps = {}) => {
    const root = queryElement(document, "[data-history-panel]", HTMLElement);
    if (!root) {
      return;
    }
    const listEl = queryElement(root, "[data-history-list]", HTMLElement);
    const statusEl = queryElement(root, "[data-history-status]", HTMLElement);
    const refreshButton = queryElement(root, "[data-history-refresh]", HTMLButtonElement);
    const undoLatestButton = queryElement(root, "[data-history-undo-latest]", HTMLButtonElement);
    const conflictBox = queryElement(root, "[data-history-conflict]", HTMLElement);
    const conflictSummaryEl = queryElement(root, "[data-history-conflict-summary]", HTMLElement);
    const forceButton = queryElement(root, "[data-history-force]", HTMLButtonElement);
    if (!listEl || !statusEl) {
      return;
    }
    const csrfToken = root.getAttribute("data-history-csrf") || "";
    const fetcher = deps.fetch || fetch;
    const confirmAction = deps.confirm || ((message) => window.confirm(message));
    let entries = [];
    let pendingConflict = null;
    let busy = false;
    const setBusy = (next) => {
      busy = next;
      root.setAttribute("aria-busy", next ? "true" : "false");
      if (refreshButton) {
        refreshButton.disabled = next;
      }
      if (undoLatestButton) {
        undoLatestButton.disabled = next || entries.length === 0;
      }
      for (const button of queryAllElements(listEl, "[data-history-undo-entry]", HTMLButtonElement)) {
        button.disabled = next;
      }
      if (forceButton) {
        forceButton.disabled = next || !pendingConflict?.entry_id;
      }
    };
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message, kind);
    };
    const hideConflict = () => {
      pendingConflict = null;
      if (conflictBox) {
        conflictBox.hidden = true;
      }
      if (conflictSummaryEl) {
        conflictSummaryEl.textContent = "";
      }
      if (forceButton) {
        forceButton.disabled = true;
      }
    };
    const showConflict = (result) => {
      pendingConflict = result;
      const conflicts = Array.isArray(result.conflicts) ? result.conflicts : [];
      if (conflictSummaryEl) {
        conflictSummaryEl.textContent = historyConflictSummary(conflicts);
      }
      if (conflictBox) {
        conflictBox.hidden = false;
      }
      if (forceButton) {
        forceButton.disabled = !result.entry_id;
      }
    };
    const renderEntries = () => {
      listEl.textContent = "";
      for (const entry of entries) {
        const item = document.createElement("li");
        item.className = "history-item";
        const main = document.createElement("div");
        main.className = "history-item-main";
        const command = document.createElement("span");
        command.className = "history-command";
        command.textContent = entry.command || entry.id;
        main.appendChild(command);
        const meta = document.createElement("span");
        meta.className = "history-meta";
        const time = formatHistoryTimestamp(entry.timestamp);
        const count = historyFileCountLabel(entry.file_count || entry.files.length);
        meta.textContent = time ? `${time} - ${count}` : count;
        main.appendChild(meta);
        const undoButton = document.createElement("button");
        undoButton.className = "history-button history-danger";
        undoButton.type = "button";
        undoButton.setAttribute("data-history-undo-entry", entry.id);
        undoButton.setAttribute("aria-label", `Undo ${entry.command || "change"}`);
        undoButton.textContent = "Undo";
        undoButton.addEventListener("click", () => {
          void runUndo(entry.id, false);
        });
        item.appendChild(main);
        item.appendChild(undoButton);
        listEl.appendChild(item);
      }
      setBusy(busy);
    };
    const loadHistory = async (successMessage = "") => {
      setBusy(true);
      setStatus("Loading history...", "is-loading");
      try {
        const resp = await fetcher("/api/history", {
          headers: { Accept: "application/json" }
        });
        const payload = await readHistoryPanelPayload(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        entries = normalizeHistoryEntries(payload);
        renderEntries();
        if (entries.length === 0) {
          setStatus("No recent changes.", "is-empty");
        } else {
          setStatus(successMessage || `${entries.length} recent changes.`, successMessage ? "is-success" : "");
        }
      } catch {
        setStatus("History unavailable.", "is-error");
      } finally {
        setBusy(false);
      }
    };
    const runUndo = async (entryID, force) => {
      if (csrfToken.trim() === "") {
        setStatus("Undo unavailable.", "is-error");
        return;
      }
      if (!force && !confirmAction("Undo this change?")) {
        setStatus("Undo canceled.");
        return;
      }
      if (force && !confirmAction("Force undo and overwrite conflicting paths?")) {
        setStatus("Force undo canceled.");
        return;
      }
      setBusy(true);
      setStatus(force ? "Forcing undo..." : "Undoing change...", "is-loading");
      try {
        const body = buildUndoForm({
          token: csrfToken,
          entryID,
          force,
          confirmForce: force
        });
        const resp = await fetcher("/api/undo", {
          method: "POST",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
            "X-BN-CSRF": csrfToken
          },
          body: body.toString()
        });
        const payload = await readHistoryPanelPayload(resp);
        const result = payload;
        if (resp.status === 409 && result?.requires_force) {
          showConflict(result);
          setStatus("Undo has conflicts.", "is-error");
          return;
        }
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        hideConflict();
        const message = result && result.ok && result.message ? result.message : "Undo restored paths.";
        await loadHistory(message);
      } catch {
        setStatus("Undo failed.", "is-error");
      } finally {
        setBusy(false);
      }
    };
    refreshButton?.addEventListener("click", () => {
      hideConflict();
      void loadHistory();
    });
    undoLatestButton?.addEventListener("click", () => {
      void runUndo("", false);
    });
    forceButton?.addEventListener("click", () => {
      const entryID = pendingConflict?.entry_id || "";
      if (entryID === "") {
        setStatus("No conflict selected.", "is-error");
        return;
      }
      void runUndo(entryID, true);
    });
    window.addEventListener(managementMutationEventName, () => {
      hideConflict();
      void loadHistory("History refreshed.");
    });
    hideConflict();
    void loadHistory();
  };
  var readHistoryPanelPayload = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };

  // internal/web/static_src/note-layout.ts
  var NOTE_META_VISIBLE_STORAGE_KEY = "bn.note-meta-sidebar.visible";
  var NOTE_META_WIDTH_STORAGE_KEY = "bn.note-meta-sidebar.width";
  var NOTE_TOC_WIDTH_STORAGE_KEY = "bn.note-toc-sidebar.width";
  var DESKTOP_BREAKPOINT_PX = 961;
  var MIN_META_WIDTH_PX = 13.5 * 16;
  var MIN_TOC_WIDTH_PX = 12 * 16;
  var MIN_CONTENT_WIDTH_PX = 20 * 16;
  var GRID_GAP_PX = 16;
  var RESIZE_STEP_PX = 16;
  var FAST_RESIZE_STEP_PX = 64;
  var safeLocalStorage2 = (view) => {
    if (!view) {
      return null;
    }
    try {
      return view.localStorage;
    } catch {
      return null;
    }
  };
  var readStoredMetaSidebarVisible = (storage) => {
    if (!storage) {
      return true;
    }
    try {
      return storage.getItem(NOTE_META_VISIBLE_STORAGE_KEY) !== "hidden";
    } catch {
      return true;
    }
  };
  var persistMetaSidebarVisible = (storage, visible) => {
    if (!storage) {
      return;
    }
    try {
      if (visible) {
        storage.removeItem(NOTE_META_VISIBLE_STORAGE_KEY);
      } else {
        storage.setItem(NOTE_META_VISIBLE_STORAGE_KEY, "hidden");
      }
    } catch {
    }
  };
  var readStoredSidebarWidth = (storage, key) => {
    if (!storage) {
      return null;
    }
    try {
      const value = Number(storage.getItem(key));
      return Number.isFinite(value) && value >= MIN_META_WIDTH_PX ? value : null;
    } catch {
      return null;
    }
  };
  var persistSidebarWidth = (storage, key, width) => {
    if (!storage || !Number.isFinite(width)) {
      return;
    }
    try {
      storage.setItem(key, String(Math.round(width)));
    } catch {
    }
  };
  var clampMetaSidebarWidth = (width, viewportWidth, hasTableOfContents) => {
    const viewportMaximum = viewportWidth / 2;
    const layoutMaximum = hasTableOfContents ? viewportWidth - MIN_TOC_WIDTH_PX - MIN_CONTENT_WIDTH_PX - 2 * GRID_GAP_PX : viewportMaximum;
    const maximum = Math.max(MIN_META_WIDTH_PX, Math.min(viewportMaximum, layoutMaximum));
    return Math.round(Math.min(Math.max(width, MIN_META_WIDTH_PX), maximum));
  };
  var isDesktop = (view) => view.innerWidth >= DESKTOP_BREAKPOINT_PX;
  var setStoredTOCWidth = (doc, layout, toc, storage) => {
    if (!toc) {
      return;
    }
    const width = Math.round(toc.getBoundingClientRect().width);
    if (width < MIN_TOC_WIDTH_PX) {
      return;
    }
    layout.style.setProperty("--bn-note-toc-sidebar-width", `${width}px`);
    doc.documentElement.style.setProperty("--bn-note-toc-sidebar-width", `${width}px`);
    persistSidebarWidth(storage, NOTE_TOC_WIDTH_STORAGE_KEY, width);
  };
  var initNoteLayout = (options = {}) => {
    const doc = options.document ?? (typeof document !== "undefined" ? document : null);
    if (!doc) {
      return;
    }
    const view = doc.defaultView;
    if (!view) {
      return;
    }
    const layout = queryElement(doc, "[data-note-layout]", HTMLElement);
    const toggle = queryElement(doc, "[data-note-sidebar-toggle]", HTMLButtonElement);
    const metadata = queryElement(doc, ".note-meta-sidebar", HTMLElement);
    if (!layout || !toggle || !metadata) {
      return;
    }
    const resizeHandle = queryElement(
      metadata,
      "[data-note-meta-resize]",
      HTMLElement
    );
    const toc = queryElement(doc, ".note-toc-sidebar", HTMLElement);
    const storage = options.storage !== void 0 ? options.storage : safeLocalStorage2(view);
    const hasTableOfContents = Boolean(toc);
    let visible = readStoredMetaSidebarVisible(storage);
    let preferredWidth = readStoredSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY);
    const applyWidth = (width) => {
      if (!isDesktop(view)) {
        layout.style.removeProperty("--bn-note-meta-width");
        return;
      }
      const renderedWidth = Math.round(metadata.getBoundingClientRect().width);
      const requestedWidth = (width ?? renderedWidth) || MIN_META_WIDTH_PX;
      const clamped = clampMetaSidebarWidth(requestedWidth, view.innerWidth, hasTableOfContents);
      if (width === null) {
        layout.style.removeProperty("--bn-note-meta-width");
      } else {
        layout.style.setProperty("--bn-note-meta-width", `${clamped}px`);
      }
      if (resizeHandle) {
        resizeHandle.setAttribute("aria-valuemin", String(MIN_META_WIDTH_PX));
        resizeHandle.setAttribute(
          "aria-valuemax",
          String(clampMetaSidebarWidth(Number.MAX_SAFE_INTEGER, view.innerWidth, hasTableOfContents))
        );
        resizeHandle.setAttribute("aria-valuenow", String(clamped));
      }
    };
    const applyVisibility = () => {
      layout.classList.toggle("is-metadata-hidden", !visible);
      metadata.hidden = !visible;
      toggle.setAttribute("aria-expanded", String(visible));
      toggle.textContent = visible ? "Hide sidebar" : "Show sidebar";
      if (visible) {
        delete doc.documentElement.dataset.noteMetaSidebar;
      } else {
        doc.documentElement.dataset.noteMetaSidebar = "hidden";
      }
    };
    const updateWidth = (width, persist) => {
      preferredWidth = clampMetaSidebarWidth(width, view.innerWidth, hasTableOfContents);
      applyWidth(preferredWidth);
      if (persist) {
        persistSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY, preferredWidth);
      }
    };
    applyWidth(preferredWidth);
    applyVisibility();
    toggle.addEventListener("click", () => {
      if (visible && isDesktop(view)) {
        setStoredTOCWidth(doc, layout, toc, storage);
      }
      visible = !visible;
      persistMetaSidebarVisible(storage, visible);
      applyVisibility();
    });
    if (resizeHandle) {
      resizeHandle.addEventListener("pointerdown", (event) => {
        if (!isDesktop(view) || event.button !== 0) {
          return;
        }
        event.preventDefault();
        doc.documentElement.classList.add("is-resizing-note-sidebar");
        const onPointerMove = (moveEvent) => {
          const layoutLeft = layout.getBoundingClientRect().left;
          updateWidth(moveEvent.clientX - layoutLeft, false);
        };
        const finishResize = () => {
          doc.documentElement.classList.remove("is-resizing-note-sidebar");
          if (preferredWidth !== null) {
            persistSidebarWidth(storage, NOTE_META_WIDTH_STORAGE_KEY, preferredWidth);
          }
          view.removeEventListener("pointermove", onPointerMove);
          view.removeEventListener("pointerup", finishResize);
          view.removeEventListener("pointercancel", finishResize);
        };
        view.addEventListener("pointermove", onPointerMove);
        view.addEventListener("pointerup", finishResize, { once: true });
        view.addEventListener("pointercancel", finishResize, { once: true });
      });
      resizeHandle.addEventListener("keydown", (event) => {
        if (!isDesktop(view) || event.key !== "ArrowLeft" && event.key !== "ArrowRight") {
          return;
        }
        event.preventDefault();
        const current = preferredWidth ?? Math.round(metadata.getBoundingClientRect().width) ?? MIN_META_WIDTH_PX;
        const step = event.shiftKey ? FAST_RESIZE_STEP_PX : RESIZE_STEP_PX;
        updateWidth(current + (event.key === "ArrowRight" ? step : -step), true);
      });
    }
    view.addEventListener("resize", () => applyWidth(preferredWidth));
  };

  // internal/web/static_src/note-insight.ts
  var disclosureMessage = [
    "Ask AI sends this note, related-note context, and your optional prompt to your configured [agent].command.",
    "BrainNotes does not store the prompt or AI output. Continue?"
  ].join("\n");
  var postRequest = (csrfToken, body) => ({
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": csrfToken
    },
    body
  });
  var readResponsePayload3 = async (response) => {
    try {
      if ((response.headers.get("Content-Type") || "").includes("application/json")) {
        return await response.json();
      }
      return await response.text();
    } catch {
      return "";
    }
  };
  var initNoteInsight = (deps = {}) => {
    const root = queryElement(document, "[data-note-insight]", HTMLElement);
    if (!root) {
      return;
    }
    const endpoint = root.getAttribute("data-note-insight-endpoint") || "/api/note/insight";
    const key = root.getAttribute("data-note-insight-key") || "";
    const csrfToken = root.getAttribute("data-note-insight-csrf") || "";
    const enabled = (root.getAttribute("data-note-insight-enabled") || "").toLowerCase() === "true";
    const prompt = queryElement(root, "[data-note-insight-prompt]", HTMLTextAreaElement);
    const submit = queryElement(root, "[data-note-insight-submit]", HTMLButtonElement);
    const status = queryElement(root, "[data-note-insight-status]", HTMLElement);
    const result = queryElement(root, "[data-note-insight-result]", HTMLElement);
    const body = queryElement(root, "[data-note-insight-body]", HTMLElement);
    const copy = queryElement(root, "[data-note-insight-copy]", HTMLButtonElement);
    if (!prompt || !submit || !status || !result || !body || !copy) {
      return;
    }
    const fetcher = deps.fetch || fetch;
    const confirmer = deps.confirm || ((message) => window.confirm(message));
    const clipboardWrite = deps.writeClipboard || (navigator.clipboard && typeof navigator.clipboard.writeText === "function" ? (text) => navigator.clipboard.writeText(text) : void 0);
    let disclosureAccepted = false;
    let output = "";
    const setStatus = (message, kind = "") => {
      setStatusText(status, message || " ", kind, ["is-success", "is-error", "is-loading"]);
    };
    const setBusy = (busy) => {
      root.setAttribute("aria-busy", String(busy));
      prompt.disabled = busy || !enabled;
      submit.disabled = busy || !enabled;
      copy.disabled = busy || output.trim() === "";
    };
    const run = async () => {
      if (!enabled || key === "" || csrfToken === "") {
        setStatus("Ask AI is unavailable.", "is-error");
        return;
      }
      if (!disclosureAccepted) {
        disclosureAccepted = confirmer(disclosureMessage);
        if (!disclosureAccepted) {
          setStatus("Ask AI canceled.");
          return;
        }
      }
      setBusy(true);
      setStatus("Generating response...", "is-loading");
      try {
        const requestBody = buildNoteInsightForm({
          key,
          prompt: prompt.value,
          token: csrfToken
        }).toString();
        const response = await fetcher(endpoint, postRequest(csrfToken, requestBody));
        const payload = await readResponsePayload3(response);
        if (!response.ok) {
          setStatus(normalizeManagementErrorMessage(response.status, payload), "is-error");
          return;
        }
        const normalized = normalizeNoteInsightResponse(payload);
        if (!normalized.ok || !normalized.enabled) {
          setStatus(normalized.message || "Ask AI is unavailable.", "is-error");
          return;
        }
        if (normalized.key && normalized.key !== key) {
          setStatus("Note changed while the response was running. Try again.", "is-error");
          return;
        }
        output = normalized.output;
        body.innerHTML = normalized.rendered_html;
        result.hidden = false;
        setStatus("Response ready.", "is-success");
      } catch {
        setStatus("Ask AI failed.", "is-error");
      } finally {
        setBusy(false);
      }
    };
    submit.addEventListener("click", () => {
      void run();
    });
    prompt.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || !event.ctrlKey && !event.metaKey || submit.disabled) {
        return;
      }
      event.preventDefault();
      void run();
    });
    copy.addEventListener("click", async () => {
      if (output.trim() === "") {
        setStatus("Nothing to copy.", "is-error");
        return;
      }
      if (!clipboardWrite) {
        setStatus("Clipboard unavailable.", "is-error");
        return;
      }
      copy.disabled = true;
      try {
        await clipboardWrite(output);
        setStatus("Response markdown copied.", "is-success");
      } catch {
        setStatus("Copy failed.", "is-error");
      } finally {
        copy.disabled = false;
      }
    });
  };

  // internal/web/static_src/note-trash.ts
  var readTrashNoteConfig = (container) => ({
    key: container.getAttribute("data-trash-key") || "",
    csrfToken: container.getAttribute("data-trash-csrf") || "",
    confirmMessage: container.getAttribute("data-trash-confirm") || "Move this note to Trash?",
    redirectPath: container.getAttribute("data-trash-redirect") || "/"
  });
  var buildTrashNoteFetchRequest = (config) => {
    const body = buildNoteKeyMutationForm({ key: config.key, token: config.csrfToken });
    return {
      url: "/api/notes/trash",
      init: {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-BN-CSRF": config.csrfToken
        },
        body: body.toString()
      }
    };
  };
  var initTrashNoteAction = (deps = {}) => {
    const container = queryElement(document, "[data-trash-note]", HTMLElement);
    if (!container) {
      return;
    }
    const button = queryElement(container, "[data-trash-button]", HTMLButtonElement);
    const statusEl = queryElement(container, "[data-trash-status]", HTMLElement);
    if (!button || !statusEl) {
      return;
    }
    const config = readTrashNoteConfig(container);
    const fetcher = deps.fetch || fetch;
    const confirmAction = deps.confirm || ((message) => window.confirm(message));
    const redirect = deps.redirect || ((path) => window.location.assign(path));
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };
    button.addEventListener("click", async () => {
      if (!config.key || !config.csrfToken) {
        setStatus("Trash action unavailable.", "is-error");
        return;
      }
      if (!confirmAction(config.confirmMessage)) {
        setStatus("Trash canceled.");
        return;
      }
      button.disabled = true;
      setStatus("Moving note to Trash...");
      try {
        const request = buildTrashNoteFetchRequest(config);
        const resp = await fetcher(request.url, request.init);
        const payload = await readTrashResponsePayload(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = payload;
        const message = result && result.ok && result.message ? result.message : "Moved note to Trash.";
        setStatus(`${message} Redirecting...`, "is-success");
        redirect(config.redirectPath);
      } catch {
        setStatus("Trash failed.", "is-error");
      } finally {
        button.disabled = false;
      }
    });
  };
  var readTrashResponsePayload = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };

  // internal/web/static_src/overview-search-elements.ts
  var resolveOverviewSearchElements = () => {
    const root = queryElement(document, "[data-overview-search-results]", HTMLElement);
    if (!root) {
      return null;
    }
    const input = getElementByID("search-input", HTMLInputElement);
    const statusEl = queryElement(root, "[data-overview-search-status]", HTMLElement);
    const listEl = queryElement(root, "[data-overview-search-list]", HTMLElement);
    if (!statusEl || !listEl) {
      return null;
    }
    const bulkRoot = queryElement(root, "[data-overview-bulk]", HTMLElement);
    const previewPanel = bulkRoot ? queryElement(bulkRoot, "[data-overview-bulk-preview]", HTMLElement) : null;
    const moveForm = bulkRoot ? queryElement(bulkRoot, "[data-bulk-move]", HTMLFormElement) : null;
    const setForm = bulkRoot ? queryElement(bulkRoot, "[data-bulk-frontmatter-set]", HTMLFormElement) : null;
    const unsetForm = bulkRoot ? queryElement(bulkRoot, "[data-bulk-frontmatter-unset]", HTMLFormElement) : null;
    return {
      root,
      input,
      statusEl,
      listEl,
      bulkRoot,
      selectedCountEl: bulkRoot ? queryElement(bulkRoot, "[data-overview-selected-count]", HTMLElement) : null,
      bulkStatusEl: bulkRoot ? queryElement(bulkRoot, "[data-overview-bulk-status]", HTMLElement) : null,
      previewPanel,
      previewTitleEl: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-title]", HTMLElement) : null,
      previewMessageEl: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-message]", HTMLElement) : null,
      previewCountsEl: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-counts]", HTMLElement) : null,
      previewResultsEl: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-results]", HTMLElement) : null,
      previewFailuresEl: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-failures]", HTMLElement) : null,
      previewApplyButton: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-apply]", HTMLButtonElement) : null,
      previewClearButton: previewPanel ? queryElement(previewPanel, "[data-overview-bulk-preview-clear]", HTMLButtonElement) : null,
      csrfToken: bulkRoot?.getAttribute("data-overview-csrf") || "",
      trashButton: bulkRoot ? queryElement(bulkRoot, "[data-bulk-trash]", HTMLButtonElement) : null,
      moveForm,
      moveButton: moveForm ? queryElement(moveForm, 'button[type="submit"]', HTMLButtonElement) : null,
      setForm,
      setButton: setForm ? queryElement(setForm, 'button[type="submit"]', HTMLButtonElement) : null,
      unsetForm,
      unsetButton: unsetForm ? queryElement(unsetForm, 'button[type="submit"]', HTMLButtonElement) : null
    };
  };

  // internal/web/static_src/overview-search-model.ts
  var overviewSearchEmptyMessage = (query) => query ? `No notes match "${query}".` : "No notes match the active filters.";
  var bulkApplyFallbackMessage = (action) => {
    switch (action) {
      case "trash":
        return "Moved selected notes to Trash.";
      case "move":
        return "Moved selected notes.";
      case "frontmatter_set":
      case "frontmatter_unset":
        return "Updated selected notes.";
      default:
        return "Applied selected notes.";
    }
  };
  var previewHeading = (action) => {
    switch (action) {
      case "trash":
        return "Trash preview";
      case "move":
        return "Move preview";
      case "frontmatter_set":
        return "Frontmatter set preview";
      case "frontmatter_unset":
        return "Frontmatter unset preview";
      default:
        return "Bulk preview";
    }
  };
  var bulkActionLabel = (action, phase) => {
    switch (action) {
      case "trash":
        return phase === "preview" ? "Preview Trash" : "Apply Trash";
      case "move":
        return phase === "preview" ? "Preview Move" : "Apply Move";
      case "frontmatter_set":
        return phase === "preview" ? "Preview Set" : "Apply Set";
      case "frontmatter_unset":
        return phase === "preview" ? "Preview Unset" : "Apply Unset";
      default:
        return phase === "preview" ? "Preview" : "Apply";
    }
  };
  var normalizeBulkFrontmatterOp = (value) => {
    if (value === "+=" || value === "-=" || value === "=") {
      return value;
    }
    return void 0;
  };
  var normalizeBulkFields = (fields) => {
    const out = {};
    if (typeof fields.folder === "string") {
      out.folder = fields.folder.trim();
    }
    if (typeof fields.property === "string") {
      out.property = fields.property.trim();
    }
    if (typeof fields.value === "string") {
      out.value = fields.value;
    }
    const op = normalizeBulkFrontmatterOp(fields.op);
    if (op) {
      out.op = op;
    }
    return out;
  };
  var previewFingerprint = (action, fields, keys) => JSON.stringify({
    action,
    keys,
    ...normalizeBulkFields(fields)
  });
  var readMoveFields = (form) => {
    if (!form) {
      return {};
    }
    const formData = new FormData(form);
    return {
      folder: String(formData.get("folder") || "").trim()
    };
  };
  var readSetFields = (form) => {
    if (!form) {
      return {};
    }
    const formData = new FormData(form);
    return {
      property: String(formData.get("property") || "").trim(),
      op: String(formData.get("op") || "="),
      value: String(formData.get("value") || "")
    };
  };
  var readUnsetFields = (form) => {
    if (!form) {
      return {};
    }
    const formData = new FormData(form);
    return {
      property: String(formData.get("property") || "").trim()
    };
  };
  var bulkStatusMessage = (result, fallback) => {
    const message = result && result.ok && result.message ? result.message : fallback;
    const failures = Array.isArray(result.failures) ? result.failures : [];
    if (failures.length === 0) {
      return message;
    }
    const summary = failures.slice(0, 3).map((failure) => {
      const key = failure.key || failure.rel_path || "note";
      return `${key}: ${failure.message || failure.code || "failed"}`;
    });
    const suffix = failures.length > summary.length ? `; ${failures.length - summary.length} more` : "";
    return `${message} Failures: ${summary.join("; ")}${suffix}.`;
  };

  // internal/web/static_src/overview-search-render.ts
  var formatPreviewValue = (value) => {
    if (!value.present) {
      return "(absent)";
    }
    if (typeof value.value === "string") {
      return JSON.stringify(value.value);
    }
    if (typeof value.value === "number" || typeof value.value === "boolean" || value.value === null) {
      return String(value.value);
    }
    try {
      return JSON.stringify(value.value);
    } catch {
      return String(value.value);
    }
  };
  var renderPreviewFrontmatterItem = (listEl, change) => {
    const doc = listEl.ownerDocument;
    const item = doc.createElement("li");
    item.className = "overview-bulk-preview-frontmatter-item";
    if (!change.changed) {
      item.classList.add("is-unchanged");
    }
    item.textContent = `${change.key}: ${formatPreviewValue(change.before)} -> ${formatPreviewValue(change.after)}`;
    listEl.appendChild(item);
  };
  var renderPreviewResultRow = (listEl, result) => {
    const doc = listEl.ownerDocument;
    const item = doc.createElement("li");
    item.className = "overview-bulk-preview-result";
    if (!result.changed) {
      item.classList.add("is-unchanged");
    }
    const header = doc.createElement("div");
    header.className = "overview-bulk-preview-result-header";
    const path = doc.createElement("p");
    path.className = "overview-bulk-preview-path";
    const sourcePath = result.source.rel_path || result.rel_path || result.key;
    const destinationPath = result.destination?.rel_path || result.rel_path;
    path.textContent = destinationPath && destinationPath !== sourcePath ? `${sourcePath} -> ${destinationPath}` : sourcePath;
    header.appendChild(path);
    const status = doc.createElement("span");
    status.className = "overview-bulk-preview-result-state";
    status.textContent = result.changed ? "Will change" : "No change";
    header.appendChild(status);
    item.appendChild(header);
    if (result.move) {
      const move = doc.createElement("p");
      move.className = "overview-bulk-preview-detail";
      move.textContent = `${result.move.rewrite_count} rewrites across ${result.move.rewrite_file_count} files (body ${result.move.body_rewrite_count}, frontmatter ${result.move.frontmatter_rewrite_count})`;
      item.appendChild(move);
    }
    if (result.frontmatter.length > 0) {
      const frontmatterList = doc.createElement("ul");
      frontmatterList.className = "overview-bulk-preview-frontmatter";
      for (const change of result.frontmatter) {
        renderPreviewFrontmatterItem(frontmatterList, change);
      }
      item.appendChild(frontmatterList);
    }
    listEl.appendChild(item);
  };
  var renderPreviewFailureItem = (listEl, failure) => {
    const doc = listEl.ownerDocument;
    const item = doc.createElement("li");
    item.className = "overview-bulk-preview-failure";
    const label = failure.rel_path || failure.key || "note";
    item.textContent = `${label}: ${failure.message || failure.code || "failed"}`;
    listEl.appendChild(item);
  };
  var renderPreviewCounts = (previewCountsEl, result) => {
    if (!previewCountsEl) {
      return;
    }
    const doc = previewCountsEl.ownerDocument;
    previewCountsEl.textContent = "";
    const counts = [
      ["Changed", result.changed || 0],
      ["Unchanged", result.unchanged || 0],
      ["Failed", result.failed || 0]
    ];
    for (const [label, value] of counts) {
      const row = doc.createElement("div");
      row.className = "overview-bulk-preview-count";
      const term = doc.createElement("dt");
      term.textContent = label;
      const detail = doc.createElement("dd");
      detail.textContent = String(value);
      row.append(term, detail);
      previewCountsEl.appendChild(row);
    }
  };
  var renderPreviewPanel = (els, previewState) => {
    const {
      previewPanel,
      previewTitleEl,
      previewMessageEl,
      previewCountsEl,
      previewResultsEl,
      previewFailuresEl,
      previewApplyButton
    } = els;
    if (!previewPanel) {
      return;
    }
    if (!previewState) {
      previewPanel.hidden = true;
      if (previewCountsEl) {
        previewCountsEl.textContent = "";
      }
      if (previewResultsEl) {
        previewResultsEl.textContent = "";
      }
      if (previewFailuresEl) {
        previewFailuresEl.textContent = "";
      }
      if (previewApplyButton) {
        previewApplyButton.textContent = "Apply";
      }
      return;
    }
    previewPanel.hidden = false;
    if (previewTitleEl) {
      previewTitleEl.textContent = previewHeading(previewState.action);
    }
    if (previewMessageEl) {
      previewMessageEl.textContent = previewState.result.message || "Preview ready.";
    }
    renderPreviewCounts(previewCountsEl, previewState.result);
    if (previewResultsEl) {
      previewResultsEl.textContent = "";
      for (const result of previewState.result.results || []) {
        renderPreviewResultRow(previewResultsEl, result);
      }
    }
    if (previewFailuresEl) {
      previewFailuresEl.textContent = "";
      const failures = previewState.result.failures || [];
      previewFailuresEl.hidden = failures.length === 0;
      for (const failure of failures) {
        renderPreviewFailureItem(previewFailuresEl, failure);
      }
    }
    if (previewApplyButton) {
      previewApplyButton.textContent = bulkActionLabel(previewState.action, "apply");
    }
  };
  var syncPreviewAttributes = (bulkRoot, previewState) => {
    if (!bulkRoot) {
      return;
    }
    if (!previewState) {
      bulkRoot.removeAttribute("data-overview-preview-ready");
      bulkRoot.removeAttribute("data-overview-preview-action");
      bulkRoot.removeAttribute("data-overview-preview-id");
      bulkRoot.removeAttribute("data-overview-preview-expires-at");
      return;
    }
    bulkRoot.setAttribute("data-overview-preview-ready", "true");
    bulkRoot.setAttribute("data-overview-preview-action", previewState.action);
    bulkRoot.setAttribute("data-overview-preview-id", previewState.previewID);
    if (previewState.expiresAt) {
      bulkRoot.setAttribute("data-overview-preview-expires-at", previewState.expiresAt);
    } else {
      bulkRoot.removeAttribute("data-overview-preview-expires-at");
    }
  };
  var renderOverviewSearchResult = (listEl, result, options = {}) => {
    const path = searchResultPath(result);
    if (!path) {
      return false;
    }
    const doc = listEl.ownerDocument;
    const item = doc.createElement("li");
    item.className = "overview-result-item";
    if (result.key) {
      item.setAttribute("data-overview-result-key", result.key);
    }
    const row = doc.createElement("div");
    row.className = "overview-result-row";
    if (result.key) {
      row.classList.add("has-selection");
      const selectWrap = doc.createElement("label");
      selectWrap.className = "overview-result-select";
      const checkbox = doc.createElement("input");
      checkbox.type = "checkbox";
      checkbox.value = result.key;
      checkbox.checked = Boolean(options.selected);
      checkbox.setAttribute(
        "aria-label",
        `Select ${result.title || result.key || "note"}`
      );
      checkbox.addEventListener("change", () => {
        if (options.onSelectionChange) {
          options.onSelectionChange(result, checkbox.checked);
        }
      });
      selectWrap.appendChild(checkbox);
      row.appendChild(selectWrap);
    }
    const link = doc.createElement("a");
    link.className = "overview-result-link";
    link.setAttribute("href", path);
    const title = doc.createElement("span");
    title.className = "overview-result-title";
    title.textContent = result.title || result.key || "Untitled";
    link.appendChild(title);
    const meta = doc.createElement("span");
    meta.className = "overview-result-meta";
    const folder = doc.createElement("span");
    folder.textContent = result.folder ? `Folder: ${result.folder}` : "Folder: /";
    meta.appendChild(folder);
    if (result.matched_fields.length > 0) {
      const fields = doc.createElement("span");
      fields.className = "overview-result-badge";
      fields.textContent = `Matched: ${result.matched_fields.join(", ")}`;
      meta.appendChild(fields);
    }
    for (const issue of result.issues) {
      const badge = doc.createElement("span");
      badge.className = "overview-result-badge is-issue";
      badge.textContent = issue.replace(/_/g, " ");
      meta.appendChild(badge);
    }
    link.appendChild(meta);
    if (result.snippet) {
      const snippet = doc.createElement("p");
      snippet.className = "overview-result-snippet";
      snippet.innerHTML = result.snippet;
      link.appendChild(snippet);
    }
    row.appendChild(link);
    item.appendChild(row);
    listEl.appendChild(item);
    return true;
  };

  // internal/web/static_src/overview-search-requests.ts
  var readBulkResponsePayload = async (resp) => {
    const contentType = resp.headers?.get("Content-Type") || "";
    try {
      if (contentType.includes("application/json")) {
        return await resp.json();
      }
      return await resp.text();
    } catch {
      return "";
    }
  };
  var bulkRequestInit = (csrfToken, body) => ({
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": csrfToken
    },
    body: body.toString()
  });
  var postBulkPreview = async (fetcher, csrfToken, action, keys, fields) => {
    const body = buildBulkPreviewForm({
      action,
      keys,
      token: csrfToken,
      folder: fields.folder,
      property: fields.property,
      op: normalizeBulkFrontmatterOp(fields.op),
      value: fields.value
    });
    const resp = await fetcher("/api/notes/bulk/preview", bulkRequestInit(csrfToken, body));
    return { resp, payload: await readBulkResponsePayload(resp) };
  };
  var postBulkApply = async (fetcher, csrfToken, action, keys, fields, previewID) => {
    const body = buildBulkApplyForm({
      action,
      keys,
      token: csrfToken,
      folder: fields.folder,
      property: fields.property,
      op: normalizeBulkFrontmatterOp(fields.op),
      value: fields.value,
      previewID
    });
    const resp = await fetcher("/api/notes/bulk", bulkRequestInit(csrfToken, body));
    return { resp, payload: await readBulkResponsePayload(resp) };
  };

  // internal/web/static_src/overview-search.ts
  var initOverviewSearchResults = (deps = {}) => {
    const els = resolveOverviewSearchElements();
    if (!els) {
      return;
    }
    const {
      root,
      input,
      statusEl,
      listEl,
      bulkRoot,
      selectedCountEl,
      bulkStatusEl,
      previewApplyButton,
      previewClearButton,
      csrfToken,
      trashButton,
      moveForm,
      moveButton,
      setForm,
      setButton,
      unsetForm,
      unsetButton
    } = els;
    const fetcher = deps.fetch || fetch;
    const selectedKeys = /* @__PURE__ */ new Map();
    let bulkBusy = false;
    let previewState = null;
    let debounceToken = 0;
    let ignoreNextManagementMutationClear = false;
    let requestToken = 0;
    const currentQuery = () => input instanceof HTMLInputElement ? normalizeParamValue(input.value) : readPageQuery();
    const setBusy = (busy) => {
      root.setAttribute("aria-busy", busy ? "true" : "false");
    };
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message, kind, ["is-loading", "is-empty", "is-error"]);
    };
    const setBulkStatus = (message, kind = "") => {
      setStatusText(bulkStatusEl, message || " ", kind, [
        "is-success",
        "is-error",
        "is-loading"
      ]);
    };
    const selectedKeyList = () => normalizeBulkNoteKeys(Array.from(selectedKeys.keys()));
    const previewMatches = (action, fields, keys = selectedKeyList()) => Boolean(
      previewState && previewState.previewID && previewState.fingerprint === previewFingerprint(action, fields, keys)
    );
    const readFieldsForAction = (action) => {
      switch (action) {
        case "move":
          return readMoveFields(moveForm);
        case "frontmatter_set":
          return readSetFields(setForm);
        case "frontmatter_unset":
          return readUnsetFields(unsetForm);
        default:
          return {};
      }
    };
    const syncActionButton = (button, action, fields) => {
      if (!button) {
        return;
      }
      const phase = previewMatches(action, fields) ? "apply" : "preview";
      button.textContent = bulkActionLabel(action, phase);
      button.setAttribute("data-bulk-phase", phase);
    };
    const updateBulkControls = () => {
      if (!bulkRoot) {
        return;
      }
      const count = selectedKeys.size;
      bulkRoot.hidden = count === 0;
      if (selectedCountEl) {
        selectedCountEl.textContent = count === 1 ? "1 selected" : `${count} selected`;
      }
      for (const control of queryAllElements(bulkRoot, "button, input, select", HTMLElement)) {
        if (control instanceof HTMLButtonElement || control instanceof HTMLInputElement || control instanceof HTMLSelectElement) {
          control.disabled = bulkBusy || count === 0;
        }
      }
      syncActionButton(trashButton, "trash", {});
      syncActionButton(moveButton, "move", readMoveFields(moveForm));
      syncActionButton(setButton, "frontmatter_set", readSetFields(setForm));
      syncActionButton(unsetButton, "frontmatter_unset", readUnsetFields(unsetForm));
      if (previewApplyButton) {
        previewApplyButton.disabled = bulkBusy || count === 0 || previewState === null;
      }
      if (previewClearButton) {
        previewClearButton.disabled = bulkBusy || previewState === null;
      }
      renderPreviewPanel(els, previewState);
    };
    const clearPreviewState = ({ clearStatus = false } = {}) => {
      const hadPreview = previewState !== null;
      previewState = null;
      syncPreviewAttributes(bulkRoot, previewState);
      updateBulkControls();
      if (clearStatus && hadPreview) {
        setBulkStatus("");
      }
    };
    const setPreviewState = (action, fields, result) => {
      previewState = {
        action,
        fingerprint: previewFingerprint(action, fields, selectedKeyList()),
        previewID: result.preview_id || "",
        expiresAt: result.expires_at,
        result
      };
      syncPreviewAttributes(bulkRoot, previewState);
      updateBulkControls();
    };
    const setBulkBusy = (busy) => {
      bulkBusy = busy;
      updateBulkControls();
    };
    const setSelected = (result, selected) => {
      const key = normalizeBulkNoteKeys([result.key])[0] || "";
      if (key === "") {
        return;
      }
      clearPreviewState({ clearStatus: true });
      if (selected) {
        selectedKeys.set(key, result.title || key);
      } else {
        selectedKeys.delete(key);
      }
      updateBulkControls();
    };
    const pruneSelectionToRenderedKeys = (visibleKeys) => {
      let changed = false;
      for (const key of selectedKeys.keys()) {
        if (!visibleKeys.has(key)) {
          selectedKeys.delete(key);
          changed = true;
        }
      }
      if (changed) {
        clearPreviewState({ clearStatus: true });
      }
      updateBulkControls();
    };
    const clearResults = (message, kind) => {
      listEl.textContent = "";
      listEl.hidden = true;
      selectedKeys.clear();
      clearPreviewState({ clearStatus: true });
      setStatus(message, kind);
    };
    const renderResults = (payload, query) => {
      const results = normalizeSearchResults(payload);
      listEl.textContent = "";
      let renderedCount = 0;
      const visibleKeys = /* @__PURE__ */ new Set();
      for (const result of results) {
        const key = normalizeBulkNoteKeys([result.key])[0] || "";
        if (renderOverviewSearchResult(listEl, result, {
          selected: key !== "" && selectedKeys.has(key),
          onSelectionChange: setSelected
        })) {
          renderedCount += 1;
          if (key !== "") {
            visibleKeys.add(key);
          }
        }
      }
      pruneSelectionToRenderedKeys(visibleKeys);
      if (renderedCount === 0) {
        clearResults(overviewSearchEmptyMessage(query), "is-empty");
        return;
      }
      listEl.hidden = false;
      const noun = renderedCount === 1 ? "note" : "notes";
      const scope = query ? ` for "${query}"` : "";
      setStatus(`${renderedCount} matching ${noun}${scope}.`, "");
    };
    const scheduleSearch = (delay) => {
      const query = currentQuery();
      window.clearTimeout(debounceToken);
      syncPageQueryParam(query);
      clearPreviewState({ clearStatus: true });
      if (query === "" && !hasActiveSharedFilters()) {
        requestToken += 1;
        setBusy(false);
        clearResults("No query or filters selected.", "is-empty");
        return;
      }
      const localToken = ++requestToken;
      setBusy(true);
      setStatus("Loading matching notes...", "is-loading");
      debounceToken = window.setTimeout(async () => {
        try {
          const resp = await fetcher(buildSearchAPIURL(query, true), {
            headers: { Accept: "application/json" }
          });
          if (!resp.ok) {
            throw new Error("search request failed");
          }
          const payload = await resp.json();
          if (localToken !== requestToken) {
            return;
          }
          renderResults(payload, query);
        } catch {
          if (localToken === requestToken) {
            clearResults("Search unavailable.", "is-error");
          }
        } finally {
          if (localToken === requestToken) {
            setBusy(false);
          }
        }
      }, delay);
    };
    if (input instanceof HTMLInputElement) {
      input.value = readPageQuery();
      input.addEventListener("input", () => {
        scheduleSearch(150);
      });
    }
    window.addEventListener(filterChangeEventName, (event) => {
      if (!shouldHandleFilterChangeForMode(event, "search")) {
        return;
      }
      scheduleSearch(0);
    });
    window.addEventListener(managementMutationEventName, () => {
      if (ignoreNextManagementMutationClear) {
        ignoreNextManagementMutationClear = false;
        return;
      }
      clearPreviewState({ clearStatus: true });
    });
    const runBulkPreview = async (action, fields, fallbackMessage) => {
      const keys = selectedKeyList();
      if (!bulkRoot || csrfToken.trim() === "") {
        setBulkStatus("Bulk action unavailable.", "is-error");
        return;
      }
      if (keys.length === 0) {
        setBulkStatus("Select one or more notes.", "is-error");
        return;
      }
      setBulkBusy(true);
      setBulkStatus("Previewing bulk action...", "is-loading");
      try {
        const { resp, payload } = await postBulkPreview(
          fetcher,
          csrfToken,
          action,
          keys,
          fields
        );
        if (!resp.ok) {
          clearPreviewState();
          setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = normalizeBulkPreviewManagementResult(payload);
        if (!result.ok || !result.preview_id) {
          clearPreviewState();
          setBulkStatus(result.message || "Bulk preview failed.", "is-error");
          return;
        }
        setPreviewState(action, fields, result);
        const failures = Array.isArray(result.failures) ? result.failures : [];
        setBulkStatus(
          bulkStatusMessage(result, fallbackMessage),
          failures.length > 0 ? "is-error" : "is-success"
        );
      } catch {
        clearPreviewState();
        setBulkStatus("Bulk preview failed.", "is-error");
      } finally {
        setBulkBusy(false);
      }
    };
    const runBulkApply = async (action, fields, fallbackMessage) => {
      const keys = selectedKeyList();
      if (!bulkRoot || csrfToken.trim() === "") {
        setBulkStatus("Bulk action unavailable.", "is-error");
        return;
      }
      if (keys.length === 0) {
        setBulkStatus("Select one or more notes.", "is-error");
        return;
      }
      if (!previewState || !previewMatches(action, fields, keys)) {
        clearPreviewState();
        setBulkStatus("Preview the bulk action before applying it.", "is-error");
        return;
      }
      setBulkBusy(true);
      setBulkStatus("Applying bulk action...", "is-loading");
      try {
        const { resp, payload } = await postBulkApply(
          fetcher,
          csrfToken,
          action,
          keys,
          fields,
          previewState.previewID
        );
        if (!resp.ok) {
          clearPreviewState();
          setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = normalizeBulkManagementResult(payload);
        const failures = Array.isArray(result.failures) ? result.failures : [];
        setBulkStatus(
          bulkStatusMessage(result, fallbackMessage),
          failures.length > 0 ? "is-error" : "is-success"
        );
        clearPreviewState();
        selectedKeys.clear();
        updateBulkControls();
        ignoreNextManagementMutationClear = true;
        window.dispatchEvent(new CustomEvent(managementMutationEventName, {
          detail: result
        }));
        scheduleSearch(0);
      } catch {
        clearPreviewState();
        setBulkStatus("Bulk action failed.", "is-error");
      } finally {
        setBulkBusy(false);
      }
    };
    const runBulkAction = async (action, fields, fallbackMessage) => {
      if (previewMatches(action, fields)) {
        await runBulkApply(action, fields, fallbackMessage);
        return;
      }
      await runBulkPreview(action, fields, fallbackMessage);
    };
    trashButton?.addEventListener("click", () => {
      void runBulkAction("trash", {}, bulkApplyFallbackMessage("trash"));
    });
    moveForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      void runBulkAction("move", readMoveFields(moveForm), bulkApplyFallbackMessage("move"));
    });
    setForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      const fields = readSetFields(setForm);
      if (!fields.property) {
        setBulkStatus("Property is required.", "is-error");
        return;
      }
      void runBulkAction("frontmatter_set", fields, bulkApplyFallbackMessage("frontmatter_set"));
    });
    unsetForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      const fields = readUnsetFields(unsetForm);
      if (!fields.property) {
        setBulkStatus("Property is required.", "is-error");
        return;
      }
      void runBulkAction("frontmatter_unset", fields, bulkApplyFallbackMessage("frontmatter_unset"));
    });
    previewApplyButton?.addEventListener("click", () => {
      if (!previewState) {
        setBulkStatus("Preview the bulk action before applying it.", "is-error");
        return;
      }
      void runBulkApply(
        previewState.action,
        readFieldsForAction(previewState.action),
        bulkApplyFallbackMessage(previewState.action)
      );
    });
    previewClearButton?.addEventListener("click", () => {
      clearPreviewState({ clearStatus: true });
    });
    const bindPreviewInvalidation = (form) => {
      if (!form) {
        return;
      }
      form.addEventListener("input", () => {
        clearPreviewState({ clearStatus: true });
      });
      form.addEventListener("change", () => {
        clearPreviewState({ clearStatus: true });
      });
    };
    bindPreviewInvalidation(moveForm);
    bindPreviewInvalidation(setForm);
    bindPreviewInvalidation(unsetForm);
    updateBulkControls();
    scheduleSearch(0);
  };

  // internal/web/static_src/search.ts
  var searchResultTitle = (result) => result.title || result.key || "Untitled";
  var searchResultFolderLabel = (result) => result.folder ? `Folder: ${result.folder}` : "Folder: /";
  var nextSearchActiveIndex = (activeIndex, resultCount, direction) => {
    if (resultCount <= 0) {
      return -1;
    }
    if (direction === "next") {
      return activeIndex < resultCount - 1 ? activeIndex + 1 : 0;
    }
    return activeIndex > 0 ? activeIndex - 1 : resultCount - 1;
  };
  var renderSearchResultOption = (doc, result, index, handlers) => {
    const item = doc.createElement("li");
    const button = doc.createElement("button");
    button.type = "button";
    button.id = `search-option-${index}`;
    button.className = "search-result";
    button.setAttribute("role", "option");
    button.setAttribute("aria-selected", "false");
    button.setAttribute("data-search-index", String(index));
    const title = doc.createElement("span");
    title.className = "search-result-title";
    title.textContent = searchResultTitle(result);
    button.appendChild(title);
    const folder = doc.createElement("span");
    folder.className = "search-result-folder";
    folder.textContent = searchResultFolderLabel(result);
    button.appendChild(folder);
    if (result.snippet) {
      const snippet = doc.createElement("p");
      snippet.className = "search-result-snippet";
      snippet.innerHTML = result.snippet;
      button.appendChild(snippet);
    }
    button.addEventListener("mouseenter", () => {
      handlers.activate(index);
    });
    button.addEventListener("click", () => {
      handlers.choose(index);
    });
    item.appendChild(button);
    return item;
  };
  var initSearch = () => {
    const searchBox = queryElement(document, "[data-search-box]", HTMLElement);
    const input = getElementByID("search-input", HTMLInputElement);
    const resultsEl = getElementByID("search-results", HTMLElement);
    if (!searchBox || !input || !resultsEl) {
      return;
    }
    const syncSearchURL = Boolean(
      queryElement(document, '[data-facet-controls][data-facet-mode="search"]', HTMLElement)
    );
    let debounceToken = 0;
    let requestToken = 0;
    let results = [];
    let activeIndex = -1;
    const setExpanded = (expanded) => {
      input.setAttribute("aria-expanded", expanded ? "true" : "false");
    };
    const setActive = (nextIndex) => {
      activeIndex = nextIndex;
      const options = resultsEl.querySelectorAll("[data-search-index]");
      for (const option of options) {
        const index = Number(option.getAttribute("data-search-index"));
        const active = index === activeIndex;
        option.classList.toggle("is-active", active);
        option.setAttribute("aria-selected", active ? "true" : "false");
        if (active) {
          input.setAttribute("aria-activedescendant", option.id);
        }
      }
      if (activeIndex < 0) {
        input.removeAttribute("aria-activedescendant");
      }
    };
    const clearResults = () => {
      results = [];
      resultsEl.textContent = "";
      resultsEl.hidden = true;
      setExpanded(false);
      setActive(-1);
    };
    const hasActiveSearchFilters = () => syncSearchURL && hasActiveSharedFilters();
    const navigateToResult = (index) => {
      if (index < 0 || index >= results.length) {
        return;
      }
      const result = results[index];
      const path = searchResultPath(result);
      if (!path) {
        return;
      }
      window.location.assign(path);
    };
    const renderResults = () => {
      resultsEl.textContent = "";
      if (results.length === 0) {
        clearResults();
        return;
      }
      results.forEach((result, index) => {
        resultsEl.appendChild(
          renderSearchResultOption(document, result, index, {
            activate: setActive,
            choose: navigateToResult
          })
        );
      });
      resultsEl.hidden = false;
      setExpanded(true);
      setActive(-1);
    };
    const scheduleSearch = (delay) => {
      const query = input.value.trim();
      window.clearTimeout(debounceToken);
      if (syncSearchURL) {
        syncPageQueryParam(query);
      }
      if (query === "" && !hasActiveSearchFilters()) {
        requestToken += 1;
        clearResults();
        return;
      }
      const localToken = ++requestToken;
      debounceToken = window.setTimeout(async () => {
        try {
          const resp = await fetch(buildSearchAPIURL(query, syncSearchURL), {
            headers: { Accept: "application/json" }
          });
          if (!resp.ok || localToken !== requestToken) {
            return;
          }
          const payload = await resp.json();
          if (localToken !== requestToken) {
            return;
          }
          results = normalizeSearchResults(payload);
          renderResults();
        } catch {
          if (localToken === requestToken) {
            clearResults();
          }
        }
      }, delay);
    };
    if (syncSearchURL) {
      input.value = readPageQuery();
      if (input.value.trim() !== "" || hasActiveSearchFilters()) {
        scheduleSearch(0);
      }
      window.addEventListener(filterChangeEventName, (event) => {
        if (!shouldHandleFilterChangeForMode(event, "search")) {
          return;
        }
        scheduleSearch(0);
      });
    }
    input.addEventListener("input", () => {
      scheduleSearch(150);
    });
    input.addEventListener("keydown", (event) => {
      if (results.length === 0) {
        if (event.key === "Escape") {
          clearResults();
        }
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setActive(nextSearchActiveIndex(activeIndex, results.length, "next"));
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setActive(nextSearchActiveIndex(activeIndex, results.length, "previous"));
        return;
      }
      if (event.key === "Enter" && activeIndex >= 0) {
        event.preventDefault();
        navigateToResult(activeIndex);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        clearResults();
      }
    });
    document.addEventListener("click", (event) => {
      const target = event.target;
      if (target instanceof Node && !searchBox.contains(target)) {
        clearResults();
      }
    });
  };

  // internal/web/static_src/main.ts
  (function() {
    initTheme();
    initFacets();
    initOpenInEditor();
    initTrashNoteAction();
    initNoteLayout();
    initNoteInsight();
    initFrontmatterEditing();
    initBrokenCreateActions();
    initCopyControls();
    initDailyBrief();
    initGraph();
    initSearch();
    initOverviewSearchResults();
    initHistoryPanel();
    initDailyFixes();
  })();
})();
