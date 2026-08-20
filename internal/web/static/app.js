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
  var buildCaptureForm = (request) => {
    const body = new URLSearchParams();
    body.set("text", request.text);
    body.set("token", request.token);
    return body;
  };
  var normalizeCaptureResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message || "",
      detail: normalizeParamValue(readRecordValue(record, ["detail", "Detail"])),
      rel_path: normalizeParamValue(readRecordValue(record, ["rel_path", "RelPath"]))
    };
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

  // internal/web/static_src/api-pins.ts
  var notePinSetPath = "/api/pins/set";
  var notePinRemovePath = "/api/pins/remove";
  var notePinReorderPath = "/api/pins/reorder";
  var buildNotePinReorderForm = (request) => {
    const body = buildNoteKeyMutationForm(request);
    body.set("before", request.before);
    body.set("after", request.after);
    return body;
  };
  var notePinFetchRequest = (url, body, token) => ({
    url,
    init: {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": token
      },
      body: body.toString()
    }
  });
  var buildNotePinSetFetchRequest = (request) => notePinFetchRequest(notePinSetPath, buildNoteKeyMutationForm(request), request.token);
  var buildNotePinRemoveFetchRequest = (request) => notePinFetchRequest(notePinRemovePath, buildNoteKeyMutationForm(request), request.token);
  var buildNotePinReorderFetchRequest = (request) => notePinFetchRequest(notePinReorderPath, buildNotePinReorderForm(request), request.token);
  var readNotePinResponsePayload = async (resp) => {
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

  // internal/web/static_src/api-properties.ts
  var PROPERTY_ROW_FILTERS = [
    "all",
    "violations",
    "missing",
    "unsupported"
  ];
  var normalizePropertyRowFilterStatus = (value) => {
    const normalized = normalizeParamValue(value);
    return PROPERTY_ROW_FILTERS.includes(normalized) ? normalized : "all";
  };
  var normalizePropertyValue = (input) => {
    const record = asRecord(input);
    if (!record) {
      return { kind: "unsupported", text: "", items: [], supported: false };
    }
    const supported = readBoolean(record.supported);
    const items = Array.isArray(record.items) ? record.items.map((item) => normalizePropertyValue(item)) : [];
    const value = {
      kind: normalizeParamValue(record.kind) || "unsupported",
      text: typeof record.text === "string" ? record.text : normalizeParamValue(record.text),
      items,
      supported
    };
    if (supported && record.value !== void 0 && record.value !== null) {
      value.value = record.value;
    }
    return value;
  };
  var normalizePropertyValueCounts = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      return [{ value: normalizePropertyValue(record.value), notes: readCount(record.notes) }];
    });
  };
  var normalizePropertyIssues = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const kind = normalizeParamValue(record.kind);
      const detail = normalizeParamValue(record.detail);
      if (kind === "" && detail === "") {
        return [];
      }
      return [{ kind, detail }];
    });
  };
  var normalizePropertyViolationCounts = (input) => {
    const record = asRecord(input) || {};
    return {
      missing: readCount(record.missing),
      type: readCount(record.type),
      value: readCount(record.value)
    };
  };
  var normalizePropertyDefinition = (input) => {
    const record = asRecord(input);
    if (!record) {
      return void 0;
    }
    const type = normalizeParamValue(record.type);
    if (type === "") {
      return void 0;
    }
    return {
      type,
      items: normalizeParamValue(record.items) || void 0,
      required: readBoolean(record.required),
      allowed: normalizeStringArray(record.allowed),
      summary: normalizeParamValue(record.summary)
    };
  };
  var normalizePropertyProposal = (input) => {
    const record = asRecord(input);
    if (!record) {
      return void 0;
    }
    const property = normalizeParamValue(record.property);
    const type = normalizeParamValue(record.type);
    if (property === "" || type === "") {
      return void 0;
    }
    return {
      property,
      type,
      items: normalizeParamValue(record.items) || void 0,
      notes: readCount(record.notes),
      rationale: normalizeParamValue(record.rationale),
      toml: typeof record.toml === "string" ? record.toml : ""
    };
  };
  var normalizePropertyProposals = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
      const proposal = normalizePropertyProposal(item);
      return proposal ? [proposal] : [];
    });
  };
  var normalizePropertySummary = (input) => {
    const record = asRecord(input) || {};
    return {
      name: normalizeParamValue(record.name),
      path: normalizeParamValue(record.path),
      configured: readBoolean(record.configured),
      definition: normalizePropertyDefinition(record.definition),
      note_count: readCount(record.note_count),
      total_notes: readCount(record.total_notes),
      coverage: readCount(record.coverage),
      missing_required: readCount(record.missing_required),
      observed_kinds: normalizeStringArray(record.observed_kinds),
      distinct_values: readCount(record.distinct_values),
      violation_notes: readCount(record.violation_notes),
      violations: readCount(record.violations),
      violation_counts: normalizePropertyViolationCounts(record.violation_counts),
      unsupported_values: readCount(record.unsupported_values),
      unsupported_notes: readCount(record.unsupported_notes),
      issues: readCount(record.issues),
      proposal: normalizePropertyProposal(record.proposal)
    };
  };
  var normalizePropertySummaries = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
      const summary = normalizePropertySummary(item);
      return summary.name === "" ? [] : [summary];
    });
  };
  var normalizePropertyRows = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const relPath = normalizeParamValue(record.rel_path);
      if (relPath === "") {
        return [];
      }
      const present = readBoolean(record.present);
      const row = {
        rel_path: relPath,
        title: normalizeParamValue(record.title),
        folder: normalizeParamValue(record.folder),
        path: normalizeParamValue(record.path),
        hash: normalizeParamValue(record.hash),
        present,
        issues: normalizePropertyIssues(record.issues),
        unsupported: readBoolean(record.unsupported)
      };
      if (present && record.value !== void 0 && record.value !== null) {
        row.value = normalizePropertyValue(record.value);
      }
      return [row];
    });
  };
  var normalizePropertyPagination = (input) => {
    const record = asRecord(input) || {};
    return {
      limit: readCount(record.limit),
      offset: readCount(record.offset),
      total: readCount(record.total),
      returned: readCount(record.returned),
      has_more: readBoolean(record.has_more)
    };
  };
  var normalizePropertyRowFilter = (input) => {
    const record = asRecord(input) || {};
    return {
      status: normalizePropertyRowFilterStatus(record.status),
      value: typeof record.value === "string" && record.value !== "" ? record.value : void 0,
      q: normalizeParamValue(record.q) || void 0
    };
  };
  var normalizePropertyConfigStatus = (input) => {
    const record = asRecord(input) || {};
    return {
      generation: readCount(record.generation),
      source_hash: normalizeParamValue(record.source_hash),
      status: normalizeParamValue(record.status) || "ok",
      error: normalizeParamValue(record.error) || void 0,
      restart_required: normalizeStringArray(record.restart_required),
      writable: readBoolean(record.writable)
    };
  };
  var normalizePropertyTotals = (input) => {
    const record = asRecord(input) || {};
    return {
      properties: readCount(record.properties),
      configured: readCount(record.configured),
      unconfigured: readCount(record.unconfigured),
      notes: readCount(record.notes),
      violations: readCount(record.violations),
      violation_notes: readCount(record.violation_notes),
      missing_required: readCount(record.missing_required),
      unsupported_values: readCount(record.unsupported_values),
      proposals: readCount(record.proposals)
    };
  };
  var normalizePropertiesResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      generation: readCount(record.generation),
      total_notes: readCount(record.total_notes),
      totals: normalizePropertyTotals(record.totals),
      properties: normalizePropertySummaries(record.properties),
      proposals: normalizePropertyProposals(record.proposals),
      config: normalizePropertyConfigStatus(record.config)
    };
  };
  var normalizePropertyDetailResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      generation: readCount(record.generation),
      property: normalizePropertySummary(record.property),
      values: normalizePropertyValueCounts(record.values),
      rows: normalizePropertyRows(record.rows),
      pagination: normalizePropertyPagination(record.pagination),
      filter: normalizePropertyRowFilter(record.filter),
      config: normalizePropertyConfigStatus(record.config)
    };
  };
  var buildPropertyDetailURL = (endpoint, query) => {
    const params = new URLSearchParams();
    params.set("property", query.property);
    if (query.status && query.status !== "all") {
      params.set("filter", query.status);
    }
    if (query.value !== void 0 && query.value !== "") {
      params.set("value", query.value);
    }
    if (query.q !== void 0 && query.q.trim() !== "") {
      params.set("q", query.q.trim());
    }
    if (query.limit !== void 0 && query.limit > 0) {
      params.set("limit", String(query.limit));
    }
    if (query.offset !== void 0 && query.offset > 0) {
      params.set("offset", String(query.offset));
    }
    return `${endpoint}?${params.toString()}`;
  };

  // internal/web/static_src/api-properties-mutation.ts
  var PROPERTY_VALUE_ENDPOINT = "/api/properties/value";
  var PROPERTY_PREVIEW_ENDPOINT = "/api/properties/preview";
  var PROPERTY_APPLY_ENDPOINT = "/api/properties/apply";
  var PROPERTY_SCHEMA_PREVIEW_ENDPOINT = "/api/properties/schema/preview";
  var PROPERTY_SCHEMA_APPLY_ENDPOINT = "/api/properties/schema/apply";
  var CONFIG_STATUS_ENDPOINT = "/api/config/status";
  var PROPERTY_BLOCKER_REPAIR_SKIP = "repair_skip";
  var normalizePropertyBlockers = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
      const record = asRecord(item);
      if (!record) {
        return [];
      }
      const kind = normalizeParamValue(record.kind);
      if (kind === "") {
        return [];
      }
      return [{
        rel_path: normalizeParamValue(record.rel_path),
        property: normalizeParamValue(record.property),
        kind,
        detail: normalizeParamValue(record.detail),
        fatal: kind !== PROPERTY_BLOCKER_REPAIR_SKIP
      }];
    });
  };
  var propertyPlanIsBlocked = (blockers) => blockers.some((blocker) => blocker.fatal);
  var normalizePropertyConfigDiff = (input) => {
    if (!Array.isArray(input)) {
      return [];
    }
    return input.flatMap((item) => {
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
        line: readCount(record.line),
        text: typeof record.text === "string" ? record.text : ""
      }];
    });
  };
  var normalizeViolationTotals = (input) => {
    const record = asRecord(input) || {};
    return {
      missing: readCount(record.missing),
      type: readCount(record.type),
      value: readCount(record.value),
      notes: readCount(record.notes)
    };
  };
  var normalizePropertyViolationProjection = (input) => {
    const record = asRecord(input) || {};
    return {
      before: normalizeViolationTotals(record.before),
      after: normalizeViolationTotals(record.after)
    };
  };
  var normalizePropertyMutationResponse = (payload) => {
    const base = normalizeManagementResult(payload);
    const record = asRecord(payload) || {};
    return {
      ok: base.ok,
      code: base.ok ? void 0 : base.code,
      message: base.message,
      generation: readCount(record.generation),
      action: normalizeParamValue(record.action),
      property: normalizeParamValue(record.property),
      new_name: normalizeParamValue(record.new_name),
      selected: readCount(record.selected),
      affected: readCount(record.affected),
      previews: normalizeNotePreviews(record.previews),
      blockers: normalizePropertyBlockers(record.blockers),
      config_diff: normalizePropertyConfigDiff(record.config_diff),
      projected_violations: normalizePropertyViolationProjection(record.projected_violations),
      preview_id: normalizeParamValue(record.preview_id),
      expires_at: normalizeParamValue(record.expires_at),
      journal_id: normalizeParamValue(record.journal_id),
      config: normalizePropertyConfigStatus(record.config)
    };
  };
  var buildPropertyRequestInit = (token, body) => ({
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-BN-CSRF": token
    },
    body: JSON.stringify(body)
  });
  var readReceiptExpiryLabel = (expiresAt, now = /* @__PURE__ */ new Date()) => {
    if (expiresAt === "") {
      return "";
    }
    const expiry = new Date(expiresAt);
    if (Number.isNaN(expiry.getTime())) {
      return "";
    }
    const seconds = Math.round((expiry.getTime() - now.getTime()) / 1e3);
    if (seconds <= 0) {
      return "This preview has expired. Preview again to apply it.";
    }
    if (seconds < 60) {
      return `This preview expires in ${seconds} ${seconds === 1 ? "second" : "seconds"}.`;
    }
    const minutes = Math.round(seconds / 60);
    return `This preview expires in about ${minutes} ${minutes === 1 ? "minute" : "minutes"}.`;
  };
  var propertyViolationDelta = (projection) => {
    const before = projection.before.missing + projection.before.type + projection.before.value;
    const after = projection.after.missing + projection.after.type + projection.after.value;
    if (before === after) {
      return `${after} ${after === 1 ? "violation" : "violations"} before and after.`;
    }
    return `Violations ${before} -> ${after}.`;
  };
  var normalizeConfigStatusResponse = (payload) => {
    const record = asRecord(payload) || {};
    return {
      ok: readBoolean(record.ok),
      ...normalizePropertyConfigStatus(record)
    };
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
          snippet: normalizeParamValue(record.snippet),
          pinned: readBoolean(record.pinned)
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

  // internal/web/static_src/api-synthesis.ts
  var synthesisContextPath = "/api/synthesis/context";
  var synthesisGeneratePath = "/api/synthesis/generate";
  var synthesisPreviewPath = "/api/synthesis/preview";
  var synthesisApplyPath = "/api/synthesis/apply";
  var SynthesisRequestError = class extends Error {
    code;
    constructor(message, code) {
      super(message);
      this.name = "SynthesisRequestError";
      this.code = code;
    }
  };
  var synthesisRequestError = (status, payload) => {
    const result = normalizeManagementResult(payload);
    return new SynthesisRequestError(
      normalizeManagementErrorMessage(status, payload),
      result.ok ? "" : result.code || ""
    );
  };
  var buildSynthesisContextForm = (request) => {
    const body = new URLSearchParams();
    body.set("source", request.source);
    for (const candidate of request.candidates) {
      body.append("candidate", candidate);
    }
    if (request.purpose !== "") {
      body.set("purpose", request.purpose);
    }
    if (request.prompt !== "") {
      body.set("prompt", request.prompt);
    }
    body.set("token", request.token);
    return body;
  };
  var buildSynthesisContextFetchRequest = (endpoint, request) => ({
    url: endpoint || synthesisContextPath,
    init: jsonPostRequest(request.token, buildSynthesisContextForm(request).toString())
  });
  var normalizeSynthesisIdentity = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const identity = {
      key: normalizeParamValue(record.key),
      title: normalizeParamValue(record.title),
      path: normalizeParamValue(record.path),
      route: normalizeParamValue(record.route),
      folder: normalizeParamValue(record.folder)
    };
    return identity.key !== "" && identity.path !== "" && identity.route !== "" ? identity : null;
  };
  var normalizeSynthesisExcerpt = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const path = normalizeParamValue(record.path);
    return path === "" ? null : {
      path,
      start_line: readCount(record.start_line),
      end_line: readCount(record.end_line)
    };
  };
  var normalizeSynthesisManifest = (value) => {
    const record = asRecord(value) || {};
    return {
      source_path: normalizeParamValue(record.source_path),
      max_bytes: readCount(record.max_bytes),
      byte_count: readCount(record.byte_count),
      notes: Array.isArray(record.notes) ? record.notes.flatMap((note) => {
        const identity = normalizeSynthesisIdentity(note);
        return identity ? [identity] : [];
      }) : [],
      excerpts: Array.isArray(record.excerpts) ? record.excerpts.flatMap((excerpt) => {
        const normalized = normalizeSynthesisExcerpt(excerpt);
        return normalized ? [normalized] : [];
      }) : []
    };
  };
  var normalizeSynthesisContextResponse = (payload) => {
    const record = asRecord(payload);
    if (!record) {
      return null;
    }
    const anchor = normalizeSynthesisIdentity(record.anchor);
    const contextID = normalizeParamValue(record.context_id);
    if (!anchor || contextID === "") {
      return null;
    }
    const supporters = Array.isArray(record.supporters) ? record.supporters.flatMap((supporter) => {
      const identity = normalizeSynthesisIdentity(supporter);
      return identity ? [identity] : [];
    }) : [];
    return {
      context_id: contextID,
      expires_at: normalizeParamValue(record.expires_at),
      vault_id: normalizeParamValue(record.vault_id),
      agent_enabled: readBoolean(record.agent_enabled),
      purpose: normalizeParamValue(record.purpose),
      prompt: typeof record.prompt === "string" ? record.prompt : "",
      anchor,
      supporters,
      manifest: normalizeSynthesisManifest(record.manifest),
      markdown: typeof record.markdown === "string" ? record.markdown : ""
    };
  };
  var requestSynthesisContext = async (fetcher, endpoint, request) => {
    const built = buildSynthesisContextFetchRequest(endpoint, request);
    const response = await fetcher(built.url, built.init);
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw synthesisRequestError(response.status, payload);
    }
    const normalized = normalizeSynthesisContextResponse(payload);
    if (!normalized || normalized.anchor.key !== request.source || normalized.supporters.length !== request.candidates.length) {
      throw new Error("The synthesis context response did not match the request.");
    }
    return normalized;
  };
  var buildSynthesisGenerateForm = (request) => {
    const body = new URLSearchParams();
    body.set("context_id", request.context_id);
    body.set("token", request.token);
    return body;
  };
  var normalizeSynthesisGenerateResponse = (payload) => {
    const record = asRecord(payload) || {};
    return {
      enabled: readBoolean(record.enabled),
      message: normalizeParamValue(record.message),
      context_id: normalizeParamValue(record.context_id),
      output: typeof record.output === "string" ? record.output : "",
      rendered_html: typeof record.rendered_html === "string" ? record.rendered_html : ""
    };
  };
  var requestSynthesisGenerate = async (fetcher, endpoint, request) => {
    const response = await fetcher(
      endpoint || synthesisGeneratePath,
      jsonPostRequest(request.token, buildSynthesisGenerateForm(request).toString())
    );
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw synthesisRequestError(response.status, payload);
    }
    const normalized = normalizeSynthesisGenerateResponse(payload);
    if (normalized.enabled && normalized.output === "") {
      throw new Error("The generation command returned no output.");
    }
    return normalized;
  };
  var buildSynthesisPreviewForm = (request) => {
    const body = new URLSearchParams();
    body.set("context_id", request.context_id);
    body.set("title", request.title);
    body.set("folder", request.folder);
    body.set("draft", request.draft);
    body.set("token", request.token);
    return body;
  };
  var normalizeSynthesisPreviewResponse = (payload) => {
    const record = asRecord(payload);
    if (!record) {
      return null;
    }
    const previewID = normalizeParamValue(record.preview_id);
    const destination = normalizeParamValue(record.destination);
    if (previewID === "" || destination === "") {
      return null;
    }
    return {
      preview_id: previewID,
      expires_at: normalizeParamValue(record.expires_at),
      context_id: normalizeParamValue(record.context_id),
      destination,
      markdown: typeof record.markdown === "string" ? record.markdown : "",
      rendered_html: typeof record.rendered_html === "string" ? record.rendered_html : ""
    };
  };
  var requestSynthesisPreview = async (fetcher, endpoint, request) => {
    const response = await fetcher(
      endpoint || synthesisPreviewPath,
      jsonPostRequest(request.token, buildSynthesisPreviewForm(request).toString())
    );
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw synthesisRequestError(response.status, payload);
    }
    const normalized = normalizeSynthesisPreviewResponse(payload);
    if (!normalized) {
      throw new Error("The synthesis preview response was incomplete.");
    }
    return normalized;
  };
  var buildSynthesisApplyForm = (request) => {
    const body = new URLSearchParams();
    body.set("preview_id", request.preview_id);
    body.set("token", request.token);
    return body;
  };
  var normalizeSynthesisApplyResponse = (payload) => {
    const result = normalizeManagementResult(payload);
    if (!result.ok || !result.path) {
      return null;
    }
    return {
      message: result.message || "Created note.",
      key: result.key || "",
      path: result.path,
      rel_path: result.rel_path || "",
      journal_id: result.journal_id || ""
    };
  };
  var requestSynthesisApply = async (fetcher, endpoint, request) => {
    const response = await fetcher(
      endpoint || synthesisApplyPath,
      jsonPostRequest(request.token, buildSynthesisApplyForm(request).toString())
    );
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw synthesisRequestError(response.status, payload);
    }
    const normalized = normalizeSynthesisApplyResponse(payload);
    if (!normalized) {
      throw new Error("The synthesis apply response did not name the created note.");
    }
    return normalized;
  };

  // internal/web/static_src/dom.ts
  var narrowElement = (element2, constructor) => element2 instanceof constructor ? element2 : null;
  var queryElement = (root, selector, constructor) => narrowElement(root.querySelector(selector), constructor);
  var queryAllElements = (root, selector, constructor) => Array.from(root.querySelectorAll(selector)).flatMap((element2) => {
    const narrowed = narrowElement(element2, constructor);
    return narrowed ? [narrowed] : [];
  });
  var getElementByID = (id, constructor) => narrowElement(document.getElementById(id), constructor);

  // internal/web/static_src/status.ts
  var statusClassNames = [
    "is-success",
    "is-error",
    "is-loading",
    "is-empty",
    "is-warning"
  ];
  var setStatusText = (element2, message, kind = "", classNames = statusClassNames) => {
    if (!element2) {
      return;
    }
    element2.textContent = message;
    element2.classList.remove(...classNames);
    if (kind) {
      element2.classList.add(kind);
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

  // internal/web/static_src/capture.ts
  var captureEndpoint = "/api/capture";
  var recentLimit = 5;
  var unconfiguredMessage = "Quick capture is not configured. Set capture.inbox_note and capture.heading in brainnotes.toml.";
  var initCapture = (deps = {}) => {
    const root = queryElement(document, "[data-capture-root]", HTMLElement);
    if (!root) {
      return;
    }
    const input = queryElement(root, "[data-capture-input]", HTMLTextAreaElement);
    const submit = queryElement(root, "[data-capture-submit]", HTMLButtonElement);
    const status = queryElement(root, "[data-capture-status]", HTMLElement);
    const recent = queryElement(root, "[data-capture-recent]", HTMLElement);
    if (!input || !submit || !status || !recent) {
      return;
    }
    const csrfToken = root.getAttribute("data-capture-csrf") || "";
    const target = root.getAttribute("data-capture-target") || "";
    const configured = (root.getAttribute("data-capture-configured") || "").toLowerCase() === "true";
    const setStatus = (message, kind = "") => {
      setStatusText(status, message || " ", kind, ["is-success", "is-error", "is-loading"]);
    };
    if (!configured) {
      input.disabled = true;
      submit.disabled = true;
      setStatus(unconfiguredMessage, "is-error");
      return;
    }
    const fetcher = deps.fetch || fetch;
    const prependRecent = (line) => {
      const item = document.createElement("li");
      item.className = "capture-recent-item";
      item.textContent = line;
      recent.prepend(item);
      while (recent.children.length > recentLimit) {
        recent.lastElementChild?.remove();
      }
    };
    const run = async () => {
      const text = input.value;
      if (text.trim() === "") {
        setStatus("Type something to capture.", "is-error");
        return;
      }
      if (csrfToken === "") {
        setStatus("Quick capture is unavailable.", "is-error");
        return;
      }
      input.disabled = true;
      submit.disabled = true;
      setStatus("Capturing...", "is-loading");
      try {
        const body = buildCaptureForm({ text, token: csrfToken }).toString();
        const response = await fetcher(captureEndpoint, jsonPostRequest(csrfToken, body));
        const payload = await readResponsePayload(response);
        if (!response.ok) {
          setStatus(normalizeManagementErrorMessage(response.status, payload), "is-error");
          return;
        }
        const result = normalizeCaptureResponse(payload);
        if (!result.ok) {
          setStatus(result.message || "Capture failed.", "is-error");
          return;
        }
        input.value = "";
        if (result.detail) {
          prependRecent(result.detail);
        }
        setStatus(result.message || `Captured to ${target}.`, "is-success");
        window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
      } catch {
        setStatus("Capture failed.", "is-error");
      } finally {
        input.disabled = false;
        submit.disabled = false;
        input.focus();
      }
    };
    submit.addEventListener("click", () => {
      void run();
    });
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || !event.ctrlKey && !event.metaKey || submit.disabled) {
        return;
      }
      event.preventDefault();
      void run();
    });
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
    const request = deps.fetch || fetch;
    const fetcher = (input, init) => request(input, init);
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
  var IMMEDIATE_ITEM_ACTIONS = /* @__PURE__ */ new Set(["exempt_dead_end", "clean_long_tail"]);
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
      case "capture_backlog":
        return "Capture backlog";
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
      case "exempt_dead_end":
      case "clean_long_tail":
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
    if (action === "exempt_dead_end") {
      return "Marking note as an intentional dead end...";
    }
    if (action === "clean_long_tail") {
      return "Removing property from affected notes...";
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
    for (const action of task.actions) {
      if (IMMEDIATE_ITEM_ACTIONS.has(action.kind)) {
        const button = triageButton(doc, task.id, action.kind, action.label);
        button.classList.add("fixes-immediate-action");
        actions.appendChild(button);
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
        if (action === "exempt_dead_end" || action === "clean_long_tail") {
          ignoreNextMutation = true;
          window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: payload }));
        }
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

  // internal/web/static_src/graph-style.ts
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
  var describeClientError = (error) => {
    if (error instanceof Error && error.message.trim() !== "") {
      return `Ask AI failed: ${error.message}`;
    }
    return "Ask AI failed.";
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
      } catch (error) {
        setStatus(describeClientError(error), "is-error");
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

  // internal/web/static_src/note-pin.ts
  var readNotePinConfig = (container) => ({
    key: container.getAttribute("data-pin-key") || "",
    csrfToken: container.getAttribute("data-pin-csrf") || "",
    pinned: container.getAttribute("data-pin-state") === "pinned"
  });
  var buildNotePinToggleRequest = (config) => {
    const request = { key: config.key, token: config.csrfToken };
    return config.pinned ? buildNotePinRemoveFetchRequest(request) : buildNotePinSetFetchRequest(request);
  };
  var initNotePinAction = (deps = {}) => {
    const container = queryElement(document, "[data-pin-note]", HTMLElement);
    if (!container) {
      return;
    }
    const button = queryElement(container, "[data-pin-button]", HTMLButtonElement);
    const statusEl = queryElement(container, "[data-pin-status]", HTMLElement);
    if (!button || !statusEl) {
      return;
    }
    const config = readNotePinConfig(container);
    const fetcher = deps.fetch || fetch;
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };
    const applyState = (pinned) => {
      config.pinned = pinned;
      container.setAttribute("data-pin-state", pinned ? "pinned" : "unpinned");
      button.setAttribute("aria-pressed", pinned ? "true" : "false");
      button.textContent = pinned ? "Unpin" : "Pin";
    };
    button.addEventListener("click", async () => {
      if (!config.key || !config.csrfToken) {
        setStatus("Pin action unavailable.", "is-error");
        return;
      }
      const pinning = !config.pinned;
      button.disabled = true;
      setStatus(pinning ? "Pinning note..." : "Unpinning note...");
      try {
        const request = buildNotePinToggleRequest(config);
        const resp = await fetcher(request.url, request.init);
        const payload = await readNotePinResponsePayload(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = payload;
        applyState(pinning);
        const fallback = pinning ? "Pinned note." : "Unpinned note.";
        setStatus(result && result.ok && result.message ? result.message : fallback, "is-success");
      } catch {
        setStatus(pinning ? "Pin failed." : "Unpin failed.", "is-error");
      } finally {
        button.disabled = false;
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

  // internal/web/static_src/overview-search-controller.ts
  var overviewSearchQuery = (input) => input instanceof HTMLInputElement ? normalizeParamValue(input.value) : readPageQuery();
  var selectedOverviewKeys = (selectedKeys) => normalizeBulkNoteKeys(Array.from(selectedKeys.keys()));
  var setOverviewBusy = (root, busy) => {
    root.setAttribute("aria-busy", busy ? "true" : "false");
  };
  var syncOverviewActionButton = (button, action, applyReady) => {
    if (!button) {
      return;
    }
    const phase = applyReady ? "apply" : "preview";
    button.textContent = bulkActionLabel(action, phase);
    button.setAttribute("data-bulk-phase", phase);
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
  var overviewCSRFToken = (doc) => queryElement(doc, "[data-overview-csrf]", HTMLElement)?.getAttribute("data-overview-csrf") || "";
  var renderOverviewResultPinButton = (doc, result) => {
    const button = doc.createElement("button");
    button.type = "button";
    button.className = "pin-button overview-result-pin";
    button.setAttribute("data-pin-button", "");
    const applyState = (pinned) => {
      const label = pinned ? "Unpin" : "Pin";
      button.setAttribute("aria-pressed", pinned ? "true" : "false");
      button.textContent = label;
      button.setAttribute("aria-label", `${label} ${result.title || result.key}`);
    };
    applyState(Boolean(result.pinned));
    button.addEventListener("click", async () => {
      const pinned = button.getAttribute("aria-pressed") === "true";
      const token = overviewCSRFToken(doc);
      if (!token) {
        button.title = "Pin action unavailable.";
        return;
      }
      button.disabled = true;
      button.title = "";
      applyState(!pinned);
      try {
        const request = pinned ? buildNotePinRemoveFetchRequest({ key: result.key, token }) : buildNotePinSetFetchRequest({ key: result.key, token });
        const resp = await fetch(request.url, request.init);
        if (!resp.ok) {
          const payload = await readNotePinResponsePayload(resp);
          button.title = normalizeManagementErrorMessage(resp.status, payload);
          applyState(pinned);
        }
      } catch {
        button.title = pinned ? "Unpin failed." : "Pin failed.";
        applyState(pinned);
      } finally {
        button.disabled = false;
      }
    });
    return button;
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
    if (result.key) {
      row.appendChild(renderOverviewResultPinButton(doc, result));
    }
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
    const previewMatches = (action, fields, keys = selectedOverviewKeys(selectedKeys)) => Boolean(
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
      syncOverviewActionButton(trashButton, "trash", previewMatches("trash", {}));
      syncOverviewActionButton(moveButton, "move", previewMatches("move", readMoveFields(moveForm)));
      syncOverviewActionButton(setButton, "frontmatter_set", previewMatches("frontmatter_set", readSetFields(setForm)));
      syncOverviewActionButton(unsetButton, "frontmatter_unset", previewMatches("frontmatter_unset", readUnsetFields(unsetForm)));
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
        fingerprint: previewFingerprint(action, fields, selectedOverviewKeys(selectedKeys)),
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
      const query = overviewSearchQuery(input);
      window.clearTimeout(debounceToken);
      syncPageQueryParam(query);
      clearPreviewState({ clearStatus: true });
      if (query === "" && !hasActiveSharedFilters()) {
        requestToken += 1;
        setOverviewBusy(root, false);
        clearResults("No query or filters selected.", "is-empty");
        return;
      }
      const localToken = ++requestToken;
      setOverviewBusy(root, true);
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
            setOverviewBusy(root, false);
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
      const keys = selectedOverviewKeys(selectedKeys);
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
      const keys = selectedOverviewKeys(selectedKeys);
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

  // internal/web/static_src/pinned-list.ts
  var draggingClass = "is-dragging";
  var dropTargetClass = "is-drop-target";
  var pinnedItemKey = (item) => narrowElement(item, HTMLElement)?.getAttribute("data-pinned-key") || "";
  var pinnedNeighborKeys = (item) => ({
    before: pinnedItemKey(item.previousElementSibling),
    after: pinnedItemKey(item.nextElementSibling)
  });
  var initPinnedList = (deps = {}) => {
    const root = queryElement(document, "[data-pinned-root]", HTMLElement);
    if (!root) {
      return;
    }
    const list = queryElement(root, "[data-pinned-list]", HTMLElement);
    const statusEl = queryElement(root, "[data-pinned-status]", HTMLElement);
    if (!list || !statusEl) {
      return;
    }
    const csrfToken = root.getAttribute("data-pinned-csrf") || "";
    const fetcher = deps.fetch || fetch;
    const reload = deps.reload || (() => window.location.reload());
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };
    const rows = () => queryAllElements(list, "[data-pinned-item]", HTMLElement);
    const post = async (request, fallback) => {
      try {
        const resp = await fetcher(request.url, request.init);
        const payload = await readNotePinResponsePayload(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return false;
        }
        const result = payload;
        setStatus(result && result.ok && result.message ? result.message : fallback, "is-success");
        return true;
      } catch {
        setStatus("Pin request failed.", "is-error");
        return false;
      }
    };
    let moveQueue = Promise.resolve();
    let queuedMoves = 0;
    let resyncing = false;
    const sendMove = async (move) => {
      if (!move.key || !csrfToken) {
        setStatus("Reorder unavailable.", "is-error");
        return false;
      }
      setStatus("Saving pin order...");
      return post(
        buildNotePinReorderFetchRequest({
          key: move.key,
          token: csrfToken,
          before: move.before,
          after: move.after
        }),
        "Reordered pins."
      );
    };
    const enqueueMove = (item, restore) => {
      queuedMoves += 1;
      const sequence = queuedMoves;
      const neighbors = pinnedNeighborKeys(item);
      const move = {
        key: pinnedItemKey(item),
        before: neighbors.before,
        after: neighbors.after
      };
      setStatus("Saving pin order...");
      moveQueue = moveQueue.then(async () => {
        if (resyncing) {
          return;
        }
        if (await sendMove(move)) {
          return;
        }
        if (sequence === queuedMoves) {
          list.insertBefore(item, restore);
          return;
        }
        resyncing = true;
        reload();
      });
    };
    const moveByKeyboard = (item, direction) => {
      const restore = item.nextElementSibling;
      if (direction < 0) {
        const previous = item.previousElementSibling;
        if (!previous) {
          return;
        }
        list.insertBefore(item, previous);
      } else {
        const next = item.nextElementSibling;
        if (!next) {
          return;
        }
        list.insertBefore(item, next.nextElementSibling);
      }
      enqueueMove(item, restore);
    };
    const startDrag = (item, event) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const restore = item.nextElementSibling;
      item.classList.add(draggingClass);
      const clearDropTarget = () => {
        for (const row of rows()) {
          row.classList.remove(dropTargetClass);
        }
      };
      const onPointerMove = (moveEvent) => {
        const next = rows().find((row) => {
          if (row === item) {
            return false;
          }
          const rect = row.getBoundingClientRect();
          return moveEvent.clientY < rect.top + rect.height / 2;
        });
        clearDropTarget();
        if (next) {
          next.classList.add(dropTargetClass);
          list.insertBefore(item, next);
        } else {
          list.appendChild(item);
        }
      };
      const finishDrag = () => {
        item.classList.remove(draggingClass);
        clearDropTarget();
        window.removeEventListener("pointermove", onPointerMove);
        window.removeEventListener("pointerup", finishDrag);
        window.removeEventListener("pointercancel", finishDrag);
        if (item.nextElementSibling === restore) {
          return;
        }
        enqueueMove(item, restore);
      };
      window.addEventListener("pointermove", onPointerMove);
      window.addEventListener("pointerup", finishDrag, { once: true });
      window.addEventListener("pointercancel", finishDrag, { once: true });
    };
    const unpin = async (item, button) => {
      const key = pinnedItemKey(item);
      if (!key || !csrfToken) {
        setStatus("Unpin unavailable.", "is-error");
        return;
      }
      button.disabled = true;
      setStatus("Unpinning note...");
      const ok = await post(
        buildNotePinRemoveFetchRequest({ key, token: csrfToken }),
        "Unpinned note."
      );
      button.disabled = false;
      if (ok) {
        reload();
      }
    };
    for (const item of rows()) {
      const handle = queryElement(item, "[data-pinned-handle]", HTMLButtonElement);
      if (handle) {
        handle.addEventListener("pointerdown", (event) => startDrag(item, event));
        handle.addEventListener("keydown", (event) => {
          if (event.key !== "ArrowUp" && event.key !== "ArrowDown") {
            return;
          }
          event.preventDefault();
          moveByKeyboard(item, event.key === "ArrowUp" ? -1 : 1);
        });
      }
      const pinButton = queryElement(item, "[data-pin-button]", HTMLButtonElement);
      if (pinButton) {
        pinButton.addEventListener("click", () => {
          void unpin(item, pinButton);
        });
      }
    }
  };

  // internal/web/static_src/properties-model.ts
  var PROPERTY_ROW_LIMIT = 50;
  var readPropertiesConfig = (root) => ({
    endpoint: root.getAttribute("data-properties-endpoint") || "/api/properties",
    detailEndpoint: root.getAttribute("data-properties-detail-endpoint") || "/api/properties/detail",
    token: root.getAttribute("data-properties-csrf") || ""
  });
  var readPropertyDetailConfig = (root) => ({
    property: root.getAttribute("data-property-name") || "",
    detailEndpoint: root.getAttribute("data-properties-detail-endpoint") || "/api/properties/detail",
    token: root.getAttribute("data-property-csrf") || "",
    historyPath: root.getAttribute("data-property-history-path") || "/library"
  });
  var emptyDetailFilter = () => ({
    status: "all",
    value: "",
    query: "",
    offset: 0
  });
  var propertyKindLabel = (kind) => {
    switch (kind) {
      case "wikilink":
        return "Wikilink";
      case "datetime":
        return "Date and time";
      case "unsupported":
        return "Unsupported";
      case "":
        return "Unknown";
      default:
        return kind.charAt(0).toUpperCase() + kind.slice(1);
    }
  };
  var definitionLabel = (summary) => {
    if (!summary.configured) {
      return "Not configured";
    }
    if (!summary.definition) {
      return "Configured";
    }
    if (summary.definition.summary !== "") {
      return summary.definition.summary;
    }
    return summary.definition.items ? `type: ${summary.definition.type} of ${summary.definition.items}` : `type: ${summary.definition.type}`;
  };
  var coverageLabel = (summary) => {
    const percent = Math.round(summary.coverage * 100);
    return `${summary.note_count} of ${summary.total_notes} notes (${percent}%)`;
  };
  var issueSummaryLabel = (summary) => {
    const parts = [];
    if (summary.violation_counts.missing > 0) {
      parts.push(`${summary.violation_counts.missing} missing`);
    }
    if (summary.violation_counts.type > 0) {
      parts.push(`${summary.violation_counts.type} wrong type`);
    }
    if (summary.violation_counts.value > 0) {
      parts.push(`${summary.violation_counts.value} disallowed value`);
    }
    if (summary.unsupported_values > 0) {
      parts.push(`${summary.unsupported_values} unsupported`);
    }
    return parts.length === 0 ? "No issues" : parts.join(", ");
  };
  var issueLabel = (issue) => {
    const prefix = issue.kind === "missing" ? "Missing" : issue.kind === "type" ? "Wrong type" : issue.kind === "value" ? "Disallowed value" : issue.kind;
    return issue.detail === "" ? prefix : `${prefix}: ${issue.detail}`;
  };
  var valueDisplayText = (value) => {
    if (!value) {
      return "";
    }
    if (!value.supported) {
      return value.text;
    }
    if (value.items.length > 0) {
      return value.items.map((item) => item.text).join(", ");
    }
    return value.text;
  };
  var catalogMatches = (summary, filter) => {
    if (filter.issuesOnly && summary.issues === 0) {
      return false;
    }
    const needle = filter.query.trim().toLowerCase();
    if (needle === "") {
      return true;
    }
    return summary.name.toLowerCase().includes(needle);
  };
  var filterCatalog = (summaries, filter) => summaries.filter((summary) => catalogMatches(summary, filter));
  var catalogStatusMessage = (result, shown) => {
    const { totals } = result;
    if (totals.properties === 0) {
      return "No properties found.";
    }
    const scope = shown === totals.properties ? `${totals.properties} properties` : `${shown} of ${totals.properties} properties`;
    const issues = totals.violations + totals.unsupported_values;
    const detail = issues === 0 ? "no issues" : `${issues} ${issues === 1 ? "issue" : "issues"} across ${totals.violation_notes} notes`;
    return `${scope} across ${totals.notes} notes, ${detail}.`;
  };
  var paginationLabel = (pagination) => {
    if (pagination.total === 0) {
      return "No notes match this filter.";
    }
    const first = pagination.offset + 1;
    const last = pagination.offset + pagination.returned;
    return `Notes ${first}-${last} of ${pagination.total}`;
  };
  var detailStatusMessage = (result) => {
    const { property } = result;
    const parts = [coverageLabel(property)];
    if (property.distinct_values > 0) {
      parts.push(
        `${property.distinct_values} distinct ${property.distinct_values === 1 ? "value" : "values"}`
      );
    }
    parts.push(issueSummaryLabel(property).toLowerCase());
    return `${parts.join(", ")}.`;
  };
  var configStatusMessage = (config) => {
    if (config.status === "ok") {
      return "";
    }
    const detail = config.error ?? "";
    return detail === "" ? "brainnotes.toml is invalid; the last valid configuration is still in use." : `brainnotes.toml is invalid: ${detail}`;
  };
  var nextOffset = (pagination) => pagination.offset + Math.max(pagination.returned, 1);
  var previousOffset = (pagination, limit) => Math.max(pagination.offset - Math.max(limit, 1), 0);

  // internal/web/static_src/properties-render.ts
  var appendDefinitionPair = (doc, list, term, detail) => {
    const stat = doc.createElement("div");
    stat.className = "properties-stat";
    const dt = doc.createElement("dt");
    dt.textContent = term;
    const dd = doc.createElement("dd");
    dd.textContent = detail;
    stat.appendChild(dt);
    stat.appendChild(dd);
    list.appendChild(stat);
  };
  var appendBadge = (doc, parent, text, className) => {
    const badge = doc.createElement("span");
    badge.className = className;
    badge.textContent = text;
    parent.appendChild(badge);
  };
  var renderTotals = (list, totals, totalNotes) => {
    if (!list) {
      return;
    }
    const doc = list.ownerDocument;
    list.textContent = "";
    appendDefinitionPair(doc, list, "Properties", String(totals.properties));
    appendDefinitionPair(doc, list, "Configured", String(totals.configured));
    appendDefinitionPair(doc, list, "Unconfigured", String(totals.unconfigured));
    appendDefinitionPair(doc, list, "Notes", String(totalNotes));
    appendDefinitionPair(doc, list, "Violations", String(totals.violations));
    appendDefinitionPair(doc, list, "Missing required", String(totals.missing_required));
    appendDefinitionPair(doc, list, "Unsupported values", String(totals.unsupported_values));
  };
  var renderCatalog = (list, summaries) => {
    const doc = list.ownerDocument;
    list.textContent = "";
    for (const summary of summaries) {
      const item = doc.createElement("li");
      item.className = "properties-item";
      item.setAttribute("data-properties-item", "");
      item.setAttribute("data-properties-name", summary.name);
      if (summary.issues > 0) {
        item.setAttribute("data-properties-issues", String(summary.issues));
      }
      const header = doc.createElement("div");
      header.className = "properties-item-header";
      const link = doc.createElement("a");
      link.className = "properties-item-name";
      link.href = summary.path;
      link.textContent = summary.name;
      header.appendChild(link);
      appendBadge(
        doc,
        header,
        summary.configured ? "Configured" : "Unconfigured",
        summary.configured ? "properties-badge properties-badge-configured" : "properties-badge properties-badge-unconfigured"
      );
      if (summary.issues > 0) {
        appendBadge(
          doc,
          header,
          `${summary.issues} ${summary.issues === 1 ? "issue" : "issues"}`,
          "properties-badge properties-badge-issues"
        );
      }
      item.appendChild(header);
      const definition = doc.createElement("p");
      definition.className = "properties-item-definition";
      definition.textContent = definitionLabel(summary);
      item.appendChild(definition);
      const meta = doc.createElement("p");
      meta.className = "properties-item-meta";
      meta.textContent = [
        coverageLabel(summary),
        `${summary.distinct_values} distinct`,
        summary.observed_kinds.map((kind) => propertyKindLabel(kind)).join(", ") || "no values"
      ].join(" - ");
      item.appendChild(meta);
      const issues = doc.createElement("p");
      issues.className = "properties-item-issues";
      issues.textContent = issueSummaryLabel(summary);
      item.appendChild(issues);
      list.appendChild(item);
    }
  };
  var renderProposals = (section, list, proposals, onAdd) => {
    if (!section || !list) {
      return;
    }
    const doc = list.ownerDocument;
    list.textContent = "";
    section.hidden = proposals.length === 0;
    for (const proposal of proposals) {
      const item = doc.createElement("li");
      item.className = "properties-proposal-item";
      item.setAttribute("data-properties-proposal", proposal.property);
      const title = doc.createElement("h4");
      title.className = "properties-proposal-title";
      title.textContent = proposal.items ? `${proposal.property}: list of ${proposal.items}` : `${proposal.property}: ${proposal.type}`;
      item.appendChild(title);
      const rationale = doc.createElement("p");
      rationale.className = "properties-proposal-rationale";
      rationale.textContent = proposal.rationale;
      item.appendChild(rationale);
      const toml = doc.createElement("pre");
      toml.className = "properties-proposal-toml";
      toml.textContent = proposal.toml;
      item.appendChild(toml);
      if (onAdd) {
        const actions = doc.createElement("div");
        actions.className = "properties-proposal-actions";
        const add = doc.createElement("button");
        add.type = "button";
        add.className = "properties-button properties-button-primary";
        add.setAttribute("data-properties-proposal-add", proposal.property);
        add.textContent = "Add to brainnotes.toml";
        add.addEventListener("click", () => {
          onAdd(proposal);
        });
        actions.appendChild(add);
        item.appendChild(actions);
      }
      list.appendChild(item);
    }
  };
  var renderSummary = (list, summary) => {
    if (!list) {
      return;
    }
    const doc = list.ownerDocument;
    list.textContent = "";
    appendDefinitionPair(doc, list, "Coverage", coverageLabel(summary));
    appendDefinitionPair(doc, list, "Distinct values", String(summary.distinct_values));
    appendDefinitionPair(
      doc,
      list,
      "Observed kinds",
      summary.observed_kinds.map((kind) => propertyKindLabel(kind)).join(", ") || "None"
    );
    appendDefinitionPair(doc, list, "Notes with violations", String(summary.violation_notes));
    appendDefinitionPair(doc, list, "Missing required", String(summary.missing_required));
    appendDefinitionPair(doc, list, "Unsupported values", String(summary.unsupported_values));
  };
  var renderDefinition = (element2, summary) => {
    if (!element2) {
      return;
    }
    element2.textContent = definitionLabel(summary);
    element2.hidden = false;
  };
  var renderProposal = (element2, summary) => {
    if (!element2) {
      return;
    }
    const doc = element2.ownerDocument;
    element2.textContent = "";
    const proposal = summary.proposal;
    element2.hidden = !proposal;
    if (!proposal) {
      return;
    }
    const heading = doc.createElement("h3");
    heading.textContent = "Inferred definition";
    element2.appendChild(heading);
    const rationale = doc.createElement("p");
    rationale.className = "properties-proposal-rationale";
    rationale.textContent = proposal.rationale;
    element2.appendChild(rationale);
    const toml = doc.createElement("pre");
    toml.className = "properties-proposal-toml";
    toml.textContent = proposal.toml;
    element2.appendChild(toml);
  };
  var renderValues = (list, values, activeValue) => {
    if (!list) {
      return;
    }
    const doc = list.ownerDocument;
    list.textContent = "";
    for (const entry of values) {
      const item = doc.createElement("li");
      item.className = "properties-value-item";
      const button = doc.createElement("button");
      button.type = "button";
      button.className = "properties-value-button";
      button.setAttribute("data-property-value", entry.value.text);
      button.setAttribute("data-property-value-kind", entry.value.kind);
      if (entry.value.text === activeValue) {
        button.setAttribute("aria-pressed", "true");
        button.classList.add("is-active");
      } else {
        button.setAttribute("aria-pressed", "false");
      }
      if (!entry.value.supported) {
        button.disabled = true;
        button.classList.add("is-unsupported");
        button.title = "This value has no supported spelling and cannot be filtered on.";
      }
      const text = doc.createElement("span");
      text.className = "properties-value-text";
      text.textContent = entry.value.text;
      button.appendChild(text);
      const kind = doc.createElement("span");
      kind.className = "properties-value-kind";
      kind.textContent = propertyKindLabel(entry.value.kind);
      button.appendChild(kind);
      const count = doc.createElement("span");
      count.className = "properties-value-count";
      count.textContent = `${entry.notes} ${entry.notes === 1 ? "note" : "notes"}`;
      button.appendChild(count);
      item.appendChild(button);
      list.appendChild(item);
    }
  };
  var appendRowControls = (doc, item, header, row, options) => {
    const select = doc.createElement("input");
    select.type = "checkbox";
    select.className = "properties-row-select";
    select.setAttribute("data-property-row-select", row.rel_path);
    select.setAttribute("aria-label", `Select ${row.title || row.rel_path}`);
    select.checked = options.selected.has(row.rel_path);
    header.insertBefore(select, header.firstChild);
    const actions = doc.createElement("div");
    actions.className = "properties-row-actions";
    const editable = !row.unsupported && row.hash !== "";
    const edit = doc.createElement("button");
    edit.type = "button";
    edit.className = "properties-button properties-row-edit";
    edit.setAttribute("data-property-row-edit", row.rel_path);
    edit.textContent = "Edit";
    edit.disabled = !editable || !row.present;
    actions.appendChild(edit);
    if (options.listKind) {
      const add = doc.createElement("button");
      add.type = "button";
      add.className = "properties-button properties-row-add";
      add.setAttribute("data-property-row-add", row.rel_path);
      add.textContent = "Add item";
      add.disabled = !editable;
      actions.appendChild(add);
    }
    const remove = doc.createElement("button");
    remove.type = "button";
    remove.className = "properties-button properties-row-unset";
    remove.setAttribute("data-property-row-unset", row.rel_path);
    remove.textContent = "Remove";
    remove.disabled = !row.present;
    actions.appendChild(remove);
    item.appendChild(actions);
  };
  var renderRows = (list, rows, options) => {
    if (!list) {
      return;
    }
    const doc = list.ownerDocument;
    list.textContent = "";
    for (const row of rows) {
      const item = doc.createElement("li");
      item.className = "properties-row";
      item.setAttribute("data-property-row", row.rel_path);
      if (row.unsupported) {
        item.setAttribute("data-property-row-unsupported", "");
      }
      if (options) {
        item.tabIndex = -1;
        if (options.selected.has(row.rel_path)) {
          item.classList.add("is-selected");
        }
      }
      const header = doc.createElement("div");
      header.className = "properties-row-header";
      const link = doc.createElement("a");
      link.className = "properties-row-title";
      link.href = row.path;
      link.textContent = row.title || row.rel_path;
      header.appendChild(link);
      if (row.folder !== "") {
        appendBadge(doc, header, row.folder, "properties-row-folder");
      }
      item.appendChild(header);
      const value = doc.createElement("p");
      value.className = "properties-row-value";
      if (!row.present) {
        value.classList.add("is-missing");
        value.textContent = "No value";
      } else if (row.unsupported) {
        value.classList.add("is-unsupported");
        value.textContent = valueDisplayText(row.value);
      } else {
        value.textContent = valueDisplayText(row.value);
      }
      item.appendChild(value);
      if (row.issues.length > 0) {
        const issues = doc.createElement("ul");
        issues.className = "properties-row-issues";
        for (const issue of row.issues) {
          const issueItem = doc.createElement("li");
          issueItem.className = "properties-row-issue";
          issueItem.setAttribute("data-property-issue", issue.kind);
          issueItem.textContent = issueLabel(issue);
          issues.appendChild(issueItem);
        }
        item.appendChild(issues);
      }
      if (options) {
        appendRowControls(doc, item, header, row, options);
      }
      list.appendChild(item);
    }
  };
  var renderPagination = (element2, pagination) => {
    if (!element2) {
      return;
    }
    element2.textContent = paginationLabel(pagination);
  };

  // internal/web/static_src/properties-preview-dialog.ts
  var FOCUSABLE_SELECTOR = [
    "a[href]",
    "button:not([disabled])",
    "input:not([disabled])",
    "select:not([disabled])",
    "textarea:not([disabled])",
    "[tabindex]:not([tabindex='-1'])"
  ].join(",");
  var blockerLabel = (blocker) => {
    const where = blocker.rel_path || blocker.property;
    const detail = blocker.detail || blocker.kind;
    return where === "" ? detail : `${where}: ${detail}`;
  };
  var previewSummaryLabel = (result) => {
    const files = result.previews.length;
    const noteScope = result.selected === 0 ? "" : `${result.selected} ${result.selected === 1 ? "note" : "notes"} selected, `;
    const changeScope = `${files} ${files === 1 ? "file" : "files"} would change`;
    const configScope = result.config_diff.length > 0 ? ", plus brainnotes.toml" : "";
    return `${noteScope}${changeScope}${configScope}.`;
  };
  var appendHeading = (doc, parent, text) => {
    const heading = doc.createElement("h3");
    heading.className = "properties-dialog-subheading";
    heading.textContent = text;
    parent.appendChild(heading);
  };
  var renderBlockers = (doc, parent, blockers, fatal) => {
    const shown = blockers.filter((blocker) => blocker.fatal === fatal);
    if (shown.length === 0) {
      return;
    }
    appendHeading(doc, parent, fatal ? "Blocked" : "Warnings");
    const list = doc.createElement("ul");
    list.className = fatal ? "properties-dialog-blockers" : "properties-dialog-warnings";
    list.setAttribute(fatal ? "data-property-preview-blockers" : "data-property-preview-warnings", "");
    for (const blocker of shown) {
      const item = doc.createElement("li");
      item.textContent = blockerLabel(blocker);
      list.appendChild(item);
    }
    parent.appendChild(list);
  };
  var renderConfigDiff = (doc, parent, result) => {
    if (result.config_diff.length === 0) {
      return;
    }
    appendHeading(doc, parent, "brainnotes.toml");
    const block = doc.createElement("pre");
    block.className = "properties-dialog-config-diff";
    block.setAttribute("data-property-preview-config", "");
    block.textContent = result.config_diff.map((line) => {
      const marker = line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " ";
      return `${marker} ${line.text}`;
    }).join("\n");
    parent.appendChild(block);
  };
  var renderFiles = (doc, parent, result) => {
    if (result.previews.length === 0) {
      return;
    }
    appendHeading(doc, parent, "Affected notes");
    const list = doc.createElement("ul");
    list.className = "properties-dialog-files";
    list.setAttribute("data-property-preview-files", "");
    for (const preview of result.previews) {
      const item = doc.createElement("li");
      item.className = "properties-dialog-file";
      const path = doc.createElement("p");
      path.className = "properties-dialog-file-path";
      path.textContent = preview.rel_path;
      item.appendChild(path);
      const changes = doc.createElement("ul");
      changes.className = "properties-dialog-changes";
      for (const change of preview.changes) {
        const line = doc.createElement("li");
        line.textContent = previewChangeLine(change);
        changes.appendChild(line);
      }
      item.appendChild(changes);
      list.appendChild(item);
    }
    parent.appendChild(list);
  };
  var createPreviewDialog = (doc, host, deps = {}) => {
    const now = deps.now || (() => /* @__PURE__ */ new Date());
    const backdrop = doc.createElement("div");
    backdrop.className = "properties-dialog-backdrop";
    backdrop.setAttribute("data-property-dialog", "");
    backdrop.hidden = true;
    const dialog = doc.createElement("div");
    dialog.className = "properties-dialog";
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.setAttribute("aria-labelledby", "property-dialog-title");
    dialog.setAttribute("aria-describedby", "property-dialog-summary");
    backdrop.appendChild(dialog);
    const title = doc.createElement("h2");
    title.className = "properties-dialog-title";
    title.id = "property-dialog-title";
    dialog.appendChild(title);
    const summary = doc.createElement("p");
    summary.className = "properties-dialog-summary";
    summary.id = "property-dialog-summary";
    summary.setAttribute("data-property-preview-summary", "");
    dialog.appendChild(summary);
    const body = doc.createElement("div");
    body.className = "properties-dialog-body";
    dialog.appendChild(body);
    const expiry = doc.createElement("p");
    expiry.className = "properties-dialog-expiry";
    expiry.setAttribute("data-property-preview-expiry", "");
    dialog.appendChild(expiry);
    const actions = doc.createElement("div");
    actions.className = "properties-dialog-actions";
    const confirm = doc.createElement("button");
    confirm.type = "button";
    confirm.className = "properties-button properties-button-primary";
    confirm.setAttribute("data-property-preview-confirm", "");
    const cancel = doc.createElement("button");
    cancel.type = "button";
    cancel.className = "properties-button";
    cancel.setAttribute("data-property-preview-cancel", "");
    cancel.textContent = "Cancel";
    actions.append(confirm, cancel);
    dialog.appendChild(actions);
    host.appendChild(backdrop);
    let opened = false;
    let restoreFocus = null;
    let handlers = null;
    const close = () => {
      if (!opened) {
        return;
      }
      opened = false;
      handlers = null;
      backdrop.hidden = true;
      restoreFocus?.focus();
      restoreFocus = null;
    };
    const cancelDialog = () => {
      const onCancel = handlers?.onCancel;
      close();
      onCancel?.();
    };
    const trapFocus = (event) => {
      const focusable = queryAllElements(dialog, FOCUSABLE_SELECTOR, HTMLElement).filter(
        (element2) => !element2.hasAttribute("hidden")
      );
      if (focusable.length === 0) {
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = doc.activeElement;
      if (event.shiftKey && (active === first || !dialog.contains(active))) {
        event.preventDefault();
        last?.focus();
        return;
      }
      if (!event.shiftKey && active === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    dialog.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        cancelDialog();
        return;
      }
      if (event.key === "Tab") {
        trapFocus(event);
      }
    });
    backdrop.addEventListener("mousedown", (event) => {
      if (event.target === backdrop) {
        cancelDialog();
      }
    });
    cancel.addEventListener("click", () => {
      cancelDialog();
    });
    confirm.addEventListener("click", () => {
      const onConfirm = handlers?.onConfirm;
      close();
      onConfirm?.();
    });
    const open = (options) => {
      handlers = options;
      const { result } = options;
      const blocked = propertyPlanIsBlocked(result.blockers);
      title.textContent = options.title;
      summary.textContent = options.summary;
      body.textContent = "";
      renderBlockers(doc, body, result.blockers, true);
      renderBlockers(doc, body, result.blockers, false);
      renderConfigDiff(doc, body, result);
      renderFiles(doc, body, result);
      const projection = doc.createElement("p");
      projection.className = "properties-dialog-projection";
      projection.setAttribute("data-property-preview-projection", "");
      projection.textContent = propertyViolationDelta(result.projected_violations);
      body.appendChild(projection);
      const expiryLabel = blocked ? "This plan cannot be applied until every blocker is resolved." : readReceiptExpiryLabel(result.expires_at, now());
      expiry.textContent = expiryLabel;
      expiry.hidden = expiryLabel === "";
      confirm.textContent = options.confirmLabel;
      confirm.disabled = blocked || result.preview_id === "";
      const active = doc.activeElement;
      restoreFocus = active instanceof HTMLElement ? active : null;
      backdrop.hidden = false;
      opened = true;
      if (confirm.disabled) {
        cancel.focus();
      } else {
        confirm.focus();
      }
    };
    return {
      element: backdrop,
      open,
      close,
      isOpen: () => opened
    };
  };

  // internal/web/static_src/properties-workbench-requests.ts
  var readPayload = async (resp) => {
    try {
      return await resp.json();
    } catch {
      return null;
    }
  };
  var postProperty = async (fetcher, endpoint, token, body) => {
    try {
      const resp = await fetcher(endpoint, buildPropertyRequestInit(token, body));
      const payload = await readPayload(resp);
      if (!resp.ok) {
        return { ok: false, message: normalizeManagementErrorMessage(resp.status, payload) };
      }
      const result = normalizePropertyMutationResponse(payload);
      if (!result.ok) {
        return { ok: false, message: result.message || "The change was refused." };
      }
      return { ok: true, result };
    } catch {
      return { ok: false, message: "The server did not answer." };
    }
  };
  var previewPropertyMutation = (fetcher, token, body) => postProperty(fetcher, PROPERTY_PREVIEW_ENDPOINT, token, body);
  var applyPropertyMutation = (fetcher, token, previewID) => postProperty(fetcher, PROPERTY_APPLY_ENDPOINT, token, {
    preview_id: previewID
  });
  var savePropertyValue = (fetcher, token, body) => postProperty(fetcher, PROPERTY_VALUE_ENDPOINT, token, body);
  var previewPropertySchema = (fetcher, token, body) => postProperty(fetcher, PROPERTY_SCHEMA_PREVIEW_ENDPOINT, token, body);
  var applyPropertySchema = (fetcher, token, previewID) => postProperty(fetcher, PROPERTY_SCHEMA_APPLY_ENDPOINT, token, {
    preview_id: previewID
  });
  var undoJournalEntry = async (fetcher, token, entryID) => {
    try {
      const resp = await fetcher("/api/undo", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-BN-CSRF": token
        },
        body: buildUndoForm({ token, entryID }).toString()
      });
      const payload = await readPayload(resp);
      if (!resp.ok) {
        return { ok: false, message: normalizeManagementErrorMessage(resp.status, payload) };
      }
      return { ok: true, message: "Change undone." };
    } catch {
      return { ok: false, message: "Undo failed." };
    }
  };

  // internal/web/static_src/properties-value-control.ts
  var PROPERTY_SCALAR_KINDS = [
    "string",
    "wikilink",
    "date",
    "datetime",
    "int",
    "float",
    "bool"
  ];
  var PROPERTY_KINDS = [
    ...PROPERTY_SCALAR_KINDS,
    "list"
  ];
  var isPropertyScalarKind = (value) => PROPERTY_SCALAR_KINDS.includes(value);
  var isPropertyKind = (value) => PROPERTY_KINDS.includes(value);
  var DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
  var RFC3339_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
  var INT_PATTERN = /^-?\d+$/;
  var pad = (value) => String(value).padStart(2, "0");
  var utcOffsetLabel = (date) => {
    const minutes = -date.getTimezoneOffset();
    const sign = minutes < 0 ? "-" : "+";
    const absolute = Math.abs(minutes);
    return `${sign}${pad(Math.floor(absolute / 60))}:${pad(absolute % 60)}`;
  };
  var datetimeLocalToRFC3339 = (local) => {
    const trimmed = local.trim();
    if (trimmed === "" || RFC3339_PATTERN.test(trimmed)) {
      return trimmed;
    }
    const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})(:\d{2})?$/.exec(trimmed);
    if (!match) {
      return trimmed;
    }
    const seconds = match[3] || ":00";
    const parsed = /* @__PURE__ */ new Date(`${match[1]}T${match[2]}${seconds}`);
    if (Number.isNaN(parsed.getTime())) {
      return trimmed;
    }
    return `${match[1]}T${match[2]}${seconds}${utcOffsetLabel(parsed)}`;
  };
  var rfc3339ToDatetimeLocal = (text) => {
    const trimmed = text.trim();
    if (trimmed === "") {
      return "";
    }
    const parsed = new Date(trimmed);
    if (Number.isNaN(parsed.getTime())) {
      return trimmed;
    }
    return [
      `${parsed.getFullYear()}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())}`,
      `T${pad(parsed.getHours())}:${pad(parsed.getMinutes())}:${pad(parsed.getSeconds())}`
    ].join("");
  };
  var wikilinkTargetError = (text) => {
    const target = text.trim();
    if (target === "") {
      return "Enter a note title.";
    }
    if (target.includes("[") || target.includes("]")) {
      return "Enter the bare target without [[ ]].";
    }
    if (target.includes("#")) {
      return "A wikilink property cannot carry a heading.";
    }
    if (target.includes("|")) {
      return "A wikilink property cannot carry an alias.";
    }
    return "";
  };
  var scalarValueError = (kind, text) => {
    const value = text.trim();
    switch (kind) {
      case "string":
        return value === "" ? "Enter a value." : "";
      case "wikilink":
        return wikilinkTargetError(text);
      case "date":
        return DATE_PATTERN.test(value) ? "" : "Enter a date as YYYY-MM-DD.";
      case "datetime":
        return RFC3339_PATTERN.test(datetimeLocalToRFC3339(value)) ? "" : "Enter a date and time.";
      case "int":
        return INT_PATTERN.test(value) ? "" : "Enter a whole number.";
      case "float":
        return value !== "" && Number.isFinite(Number(value)) ? "" : "Enter a number.";
      case "bool":
        return value === "true" || value === "false" ? "" : "Choose true or false.";
    }
  };
  var inputTypeFor = (kind) => {
    switch (kind) {
      case "date":
        return "date";
      case "datetime":
        return "datetime-local";
      case "int":
      case "float":
        return "number";
      default:
        return "text";
    }
  };
  var seedText = (kind, text) => kind === "datetime" ? rfc3339ToDatetimeLocal(text) : text;
  var readText = (kind, raw) => kind === "datetime" ? datetimeLocalToRFC3339(raw) : raw.trim();
  var createSelect = (doc, options, choices) => {
    const select = doc.createElement("select");
    select.className = "properties-control properties-control-select";
    select.id = options.id;
    select.setAttribute("aria-label", options.label);
    select.setAttribute("data-property-control", options.kind);
    for (const choice of choices) {
      const option = doc.createElement("option");
      option.value = choice;
      option.textContent = choice;
      select.appendChild(option);
    }
    const current = options.text.trim();
    select.value = choices.includes(current) ? current : choices[0] || "";
    return select;
  };
  var createScalarControl = (doc, options) => {
    if (options.kind === "bool") {
      const select = createSelect(doc, options, ["true", "false"]);
      return {
        element: select,
        read: () => select.value,
        error: () => scalarValueError("bool", select.value),
        focus: () => select.focus()
      };
    }
    if (options.allowed.length > 0) {
      const select = createSelect(doc, options, options.allowed);
      return {
        element: select,
        read: () => select.value,
        error: () => options.allowed.includes(select.value) ? "" : "Choose an allowed value.",
        focus: () => select.focus()
      };
    }
    const input = doc.createElement("input");
    input.className = "properties-control properties-control-input";
    input.type = inputTypeFor(options.kind);
    input.id = options.id;
    input.autocomplete = "off";
    input.spellcheck = false;
    input.setAttribute("aria-label", options.label);
    input.setAttribute("data-property-control", options.kind);
    if (options.kind === "int") {
      input.step = "1";
    }
    if (options.kind === "float") {
      input.step = "any";
    }
    if (options.kind === "datetime") {
      input.step = "1";
    }
    if (options.kind === "wikilink") {
      input.placeholder = "Note title";
    }
    input.value = seedText(options.kind, options.text);
    return {
      element: input,
      read: () => readText(options.kind, input.value),
      error: () => scalarValueError(options.kind, input.value),
      focus: () => input.focus()
    };
  };
  var listSeedTexts = (options) => {
    const items = options.value?.items || [];
    if (items.length === 0) {
      return [""];
    }
    return items.map((item) => item.text);
  };
  var createListControl = (doc, options) => {
    const wrapper = doc.createElement("div");
    wrapper.className = "properties-control-list";
    wrapper.setAttribute("data-property-control", "list");
    const list = doc.createElement("ul");
    list.className = "properties-control-list-items";
    wrapper.appendChild(list);
    const controls = [];
    let sequence = 0;
    const renumber = () => {
      const rows = Array.from(list.children);
      rows.forEach((row, index) => {
        const remove = row.querySelector("[data-property-list-remove]");
        if (remove instanceof HTMLButtonElement) {
          remove.setAttribute("aria-label", `Remove item ${index + 1}`);
          remove.disabled = rows.length <= 1;
        }
      });
    };
    const addRow = (text, focusIt) => {
      sequence += 1;
      const row = doc.createElement("li");
      row.className = "properties-control-list-item";
      const control = createScalarControl(doc, {
        kind: options.items,
        allowed: options.allowed,
        text,
        id: `${options.id}-item-${sequence}`,
        label: `${options.label} item`
      });
      controls.push(control);
      row.appendChild(control.element);
      const remove = doc.createElement("button");
      remove.type = "button";
      remove.className = "properties-button properties-control-remove";
      remove.setAttribute("data-property-list-remove", "");
      remove.textContent = "Remove";
      remove.addEventListener("click", () => {
        const index = controls.indexOf(control);
        if (index >= 0) {
          controls.splice(index, 1);
        }
        row.remove();
        renumber();
      });
      row.appendChild(remove);
      list.appendChild(row);
      renumber();
      if (focusIt) {
        control.focus();
      }
    };
    for (const text of listSeedTexts(options)) {
      addRow(text, false);
    }
    const add = doc.createElement("button");
    add.type = "button";
    add.className = "properties-button properties-control-add";
    add.setAttribute("data-property-list-add", "");
    add.textContent = "Add item";
    add.addEventListener("click", () => {
      addRow("", true);
    });
    wrapper.appendChild(add);
    return {
      element: wrapper,
      read: () => ({
        kind: "list",
        text: "",
        items: controls.map((control) => ({
          kind: options.items,
          text: control.read()
        }))
      }),
      error: () => {
        if (controls.length === 0) {
          return "Add at least one item.";
        }
        for (const control of controls) {
          const message = control.error();
          if (message !== "") {
            return message;
          }
        }
        return "";
      },
      focus: () => controls[0]?.focus()
    };
  };
  var createValueControl = (doc, options) => {
    if (options.kind === "list") {
      return createListControl(doc, options);
    }
    const scalar = createScalarControl(doc, {
      kind: options.kind,
      allowed: options.allowed,
      text: options.value?.text || "",
      id: options.id,
      label: options.label
    });
    return {
      element: scalar.element,
      read: () => ({ kind: options.kind, text: scalar.read() }),
      error: () => scalar.error(),
      focus: () => scalar.focus()
    };
  };

  // internal/web/static_src/properties-catalog.ts
  var readPayload2 = async (resp) => {
    try {
      return await resp.json();
    } catch {
      return null;
    }
  };
  var buildOpenConfigFetchRequest = (token) => ({
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": token
    },
    body: new URLSearchParams({ token }).toString()
  });
  var inferredDefinition = (proposal) => {
    if (!isPropertyKind(proposal.type)) {
      return null;
    }
    if (proposal.type === "list") {
      if (!proposal.items || !isPropertyScalarKind(proposal.items)) {
        return null;
      }
      return {
        type: proposal.type,
        items: proposal.items,
        required: false,
        allowed: []
      };
    }
    return {
      type: proposal.type,
      required: false,
      allowed: []
    };
  };
  var initPropertiesCatalog = (deps = {}) => {
    const root = queryElement(document, "[data-properties-catalog]", HTMLElement);
    if (!root) {
      return;
    }
    const statusEl = queryElement(root, "[data-properties-status]", HTMLElement);
    const listEl = queryElement(root, "[data-properties-list]", HTMLElement);
    if (!statusEl || !listEl) {
      return;
    }
    const configEl = queryElement(root, "[data-properties-config]", HTMLElement);
    const totalsEl = queryElement(root, "[data-properties-totals]", HTMLElement);
    const proposalsEl = queryElement(root, "[data-properties-proposals]", HTMLElement);
    const proposalListEl = queryElement(root, "[data-properties-proposal-list]", HTMLElement);
    const configOpenEl = queryElement(root, "[data-properties-config-open]", HTMLButtonElement);
    const dialogHost = queryElement(root, "[data-properties-dialog-host]", HTMLElement);
    const queryEl = queryElement(root, "[data-properties-query]", HTMLInputElement);
    const issuesOnlyEl = queryElement(root, "[data-properties-issues-only]", HTMLInputElement);
    const refreshEl = queryElement(root, "[data-properties-refresh]", HTMLButtonElement);
    const config = readPropertiesConfig(root);
    const fetcher = deps.fetch || fetch;
    const dialog = config.token !== "" && dialogHost ? createPreviewDialog(root.ownerDocument, dialogHost) : null;
    let latest = null;
    let mutationBusy = false;
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message, kind);
    };
    const currentFilter = () => ({
      query: queryEl?.value || "",
      issuesOnly: issuesOnlyEl?.checked || false
    });
    const setMutationBusy = (busy) => {
      mutationBusy = busy;
      for (const button of root.querySelectorAll(
        "[data-properties-proposal-add]"
      )) {
        button.disabled = busy;
      }
    };
    const applyFilter = () => {
      if (!latest) {
        return;
      }
      const shown = filterCatalog(latest.properties, currentFilter());
      renderCatalog(listEl, shown);
      setStatus(
        catalogStatusMessage(latest, shown.length),
        shown.length === 0 ? "is-empty" : ""
      );
    };
    const applyProposal = async (proposal, previewID) => {
      setMutationBusy(true);
      setStatus("Adding the inferred definition...", "is-loading");
      try {
        const outcome = await applyPropertySchema(fetcher, config.token, previewID);
        if (!outcome.ok) {
          setStatus(outcome.message, "is-error");
          return;
        }
        await load();
        setStatus(`Added ${proposal.property} to brainnotes.toml.`, "is-success");
      } finally {
        setMutationBusy(false);
      }
    };
    const addProposal = async (proposal) => {
      if (mutationBusy) {
        return;
      }
      const definition = inferredDefinition(proposal);
      if (!dialog || config.token === "" || !definition) {
        setStatus("This inferred definition cannot be added.", "is-error");
        return;
      }
      setMutationBusy(true);
      setStatus("Previewing the inferred definition...", "is-loading");
      try {
        const outcome = await previewPropertySchema(fetcher, config.token, {
          op: "upsert",
          property: proposal.property,
          definition
        });
        if (!outcome.ok) {
          setStatus(outcome.message, "is-error");
          return;
        }
        const result = outcome.result;
        setStatus(previewSummaryLabel(result));
        dialog.open({
          title: `Add ${proposal.property} to brainnotes.toml`,
          summary: previewSummaryLabel(result),
          confirmLabel: "Add definition",
          result,
          onConfirm: () => {
            void applyProposal(proposal, result.preview_id);
          },
          onCancel: () => {
            setStatus("Definition change canceled.");
          }
        });
      } finally {
        setMutationBusy(false);
      }
    };
    const load = async () => {
      root.setAttribute("aria-busy", "true");
      setStatus("Loading properties...", "is-loading");
      try {
        const resp = await fetcher(config.endpoint, { headers: { Accept: "application/json" } });
        const payload = await readPayload2(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = normalizePropertiesResponse(payload);
        if (!result.ok) {
          setStatus(result.message || "Properties unavailable.", "is-error");
          return;
        }
        latest = result;
        renderTotals(totalsEl, result.totals, result.total_notes);
        renderProposals(proposalsEl, proposalListEl, result.proposals, (proposal) => {
          void addProposal(proposal);
        });
        if (configEl) {
          const message = configStatusMessage(result.config);
          configEl.textContent = message;
          configEl.hidden = message === "";
        }
        applyFilter();
      } catch {
        setStatus("Properties unavailable.", "is-error");
      } finally {
        root.setAttribute("aria-busy", "false");
      }
    };
    queryEl?.addEventListener("input", applyFilter);
    issuesOnlyEl?.addEventListener("change", applyFilter);
    refreshEl?.addEventListener("click", () => {
      void load();
    });
    configOpenEl?.addEventListener("click", async () => {
      if (config.token === "") {
        setStatus("Open action unavailable.", "is-error");
        return;
      }
      configOpenEl.disabled = true;
      setStatus("Opening brainnotes.toml in the configured editor...", "is-loading");
      try {
        const resp = await fetcher("/api/config/open", buildOpenConfigFetchRequest(config.token));
        if (!resp.ok) {
          setStatus(normalizeOpenErrorMessage(resp.status, await resp.text()), "is-error");
          return;
        }
        setStatus("Opened brainnotes.toml in the configured editor.", "is-success");
      } catch {
        setStatus("Open failed.", "is-error");
      } finally {
        configOpenEl.disabled = false;
      }
    });
    void load();
  };

  // internal/web/static_src/properties-config-status.ts
  var CONFIG_STATUS_POLL_MS = 5e3;
  var configInvalidMessage = (config) => {
    if (config.status === "ok") {
      return "";
    }
    const detail = config.error || "";
    const cause = detail === "" ? "brainnotes.toml is invalid." : `brainnotes.toml is invalid: ${detail}`;
    return `${cause} The last valid configuration is still in use and property writes are refused until it parses.`;
  };
  var configRestartMessage = (config) => {
    const keys = config.restart_required;
    if (keys.length === 0) {
      return "";
    }
    return `${keys.join(", ")} changed on disk. Restart bn serve to apply ${keys.length === 1 ? "it" : "them"}.`;
  };
  var createConfigStatusPoller = (doc, deps) => {
    const fetcher = deps.fetch || fetch;
    const intervalMs = deps.intervalMs || CONFIG_STATUS_POLL_MS;
    let timer = 0;
    let seenGeneration = -1;
    let current = null;
    const poll = async () => {
      if (doc.visibilityState !== "visible") {
        return;
      }
      try {
        const resp = await fetcher(CONFIG_STATUS_ENDPOINT, {
          headers: { Accept: "application/json" }
        });
        if (!resp.ok) {
          return;
        }
        const result = normalizeConfigStatusResponse(await resp.json());
        current = result;
        deps.onStatus(result);
        if (seenGeneration >= 0 && result.generation !== seenGeneration) {
          deps.onGenerationChange?.(result.generation);
        }
        seenGeneration = result.generation;
      } catch {
      }
    };
    const stop = () => {
      if (timer !== 0) {
        clearInterval(timer);
        timer = 0;
      }
    };
    const start = () => {
      stop();
      if (doc.visibilityState !== "visible") {
        return;
      }
      timer = setInterval(() => {
        void poll();
      }, intervalMs);
    };
    doc.addEventListener("visibilitychange", () => {
      if (doc.visibilityState === "visible") {
        void poll();
        start();
        return;
      }
      stop();
    });
    return { poll, start, stop, latest: () => current };
  };

  // internal/web/static_src/properties-schema-editor.ts
  var parseAllowedValues = (text) => {
    const seen = /* @__PURE__ */ new Set();
    const out = [];
    for (const line of text.split("\n")) {
      const value = line.trim();
      if (value === "" || seen.has(value)) {
        continue;
      }
      seen.add(value);
      out.push(value);
    }
    return out;
  };
  var definitionError = (definition) => {
    if (definition.type === "list" && !definition.items) {
      return "Choose the element kind a list holds.";
    }
    if (definition.type !== "list" && definition.items) {
      return "Only a list declares an element kind.";
    }
    return "";
  };
  var fillKindOptions = (select, kinds, blankLabel) => {
    select.textContent = "";
    if (blankLabel !== "") {
      const blank = select.ownerDocument.createElement("option");
      blank.value = "";
      blank.textContent = blankLabel;
      select.appendChild(blank);
    }
    for (const kind of kinds) {
      const option = select.ownerDocument.createElement("option");
      option.value = kind;
      option.textContent = kind;
      select.appendChild(option);
    }
  };
  var createPropertySchemaEditor = (host) => {
    const section = queryElement(host.root, "[data-property-schema]", HTMLElement);
    if (!section) {
      return null;
    }
    const typeEl = queryElement(section, "[data-property-schema-type]", HTMLSelectElement);
    const itemsEl = queryElement(section, "[data-property-schema-items]", HTMLSelectElement);
    const requiredEl = queryElement(
      section,
      "[data-property-schema-required]",
      HTMLInputElement
    );
    const allowedEl = queryElement(
      section,
      "[data-property-schema-allowed]",
      HTMLTextAreaElement
    );
    const saveEl = queryElement(section, "[data-property-schema-save]", HTMLButtonElement);
    const deleteEl = queryElement(section, "[data-property-schema-delete]", HTMLButtonElement);
    const adoptEl = queryElement(section, "[data-property-schema-adopt]", HTMLButtonElement);
    const errorEl = queryElement(section, "[data-property-schema-error]", HTMLElement);
    if (!typeEl || !itemsEl || !requiredEl || !allowedEl || !saveEl) {
      return null;
    }
    fillKindOptions(typeEl, PROPERTY_KINDS, "");
    fillKindOptions(itemsEl, PROPERTY_SCALAR_KINDS, "");
    const itemsRow = itemsEl.closest("[data-property-schema-items-row]");
    let proposed = null;
    const showError = (message) => {
      if (errorEl) {
        errorEl.textContent = message;
        errorEl.hidden = message === "";
      }
    };
    const syncItemsVisibility = () => {
      const isList = typeEl.value === "list";
      if (itemsRow) {
        itemsRow.hidden = !isList;
      }
      itemsEl.disabled = !isList;
    };
    const readDefinition = () => {
      if (!isPropertyKind(typeEl.value)) {
        showError("Choose a type.");
        return null;
      }
      const type = typeEl.value;
      const items = type === "list" && isPropertyScalarKind(itemsEl.value) ? itemsEl.value : void 0;
      const definition = {
        type,
        items,
        required: requiredEl.checked,
        allowed: parseAllowedValues(allowedEl.value)
      };
      const message = definitionError(definition);
      if (message !== "") {
        showError(message);
        return null;
      }
      showError("");
      return definition;
    };
    typeEl.addEventListener("change", syncItemsVisibility);
    saveEl.addEventListener("click", () => {
      const definition = readDefinition();
      if (definition) {
        host.submit("upsert", definition);
      }
    });
    deleteEl?.addEventListener("click", () => {
      host.submit("delete");
    });
    adoptEl?.addEventListener("click", () => {
      if (!proposed) {
        return;
      }
      typeEl.value = proposed.type;
      itemsEl.value = proposed.items || "";
      requiredEl.checked = false;
      allowedEl.value = "";
      syncItemsVisibility();
      showError("");
      host.setStatus("Inferred definition loaded. Review it, then save to preview the change.");
      typeEl.focus();
    });
    return {
      sync: (summary) => {
        const definition = summary.definition;
        typeEl.value = definition?.type || "string";
        itemsEl.value = definition?.items || "";
        requiredEl.checked = definition?.required || false;
        allowedEl.value = (definition?.allowed || []).join("\n");
        syncItemsVisibility();
        showError("");
        if (deleteEl) {
          deleteEl.disabled = !summary.configured;
        }
        proposed = summary.proposal ? { type: summary.proposal.type, items: summary.proposal.items } : null;
        if (adoptEl) {
          adoptEl.hidden = proposed === null;
        }
      }
    };
  };

  // internal/web/static_src/properties-toast.ts
  var createUndoToast = (doc, host, deps) => {
    const element2 = doc.createElement("div");
    element2.className = "properties-toast";
    element2.setAttribute("data-property-toast", "");
    element2.setAttribute("role", "status");
    element2.setAttribute("aria-live", "polite");
    element2.hidden = true;
    const message = doc.createElement("p");
    message.className = "properties-toast-message";
    message.setAttribute("data-property-toast-message", "");
    element2.appendChild(message);
    const actions = doc.createElement("div");
    actions.className = "properties-toast-actions";
    const undo = doc.createElement("button");
    undo.type = "button";
    undo.className = "properties-button properties-toast-undo";
    undo.setAttribute("data-property-toast-undo", "");
    undo.textContent = "Undo";
    actions.appendChild(undo);
    const history = doc.createElement("a");
    history.className = "properties-toast-history";
    history.setAttribute("data-property-toast-history", "");
    history.textContent = "View in history";
    actions.appendChild(history);
    const dismiss = doc.createElement("button");
    dismiss.type = "button";
    dismiss.className = "properties-button properties-toast-dismiss";
    dismiss.setAttribute("data-property-toast-dismiss", "");
    dismiss.textContent = "Dismiss";
    actions.appendChild(dismiss);
    element2.appendChild(actions);
    host.appendChild(element2);
    let entryID = "";
    const hide = () => {
      entryID = "";
      element2.hidden = true;
      message.textContent = "";
    };
    undo.addEventListener("click", () => {
      const pending = entryID;
      if (pending === "") {
        return;
      }
      hide();
      deps.onUndo(pending);
    });
    dismiss.addEventListener("click", hide);
    return {
      element: element2,
      show: (options) => {
        entryID = options.journalID;
        message.textContent = options.message;
        history.href = options.historyHref;
        undo.hidden = options.journalID === "";
        element2.hidden = false;
      },
      hide,
      journalID: () => entryID
    };
  };

  // internal/web/static_src/properties-workbench-model.ts
  var editKind = (summary) => {
    const declared = summary.definition?.type || "";
    if (isPropertyKind(declared)) {
      return declared;
    }
    const observed = summary.observed_kinds.filter((kind) => isPropertyKind(kind));
    return observed.length === 1 && observed[0] && isPropertyKind(observed[0]) ? observed[0] : "string";
  };
  var editItemKind = (summary) => {
    const declared = summary.definition?.items || "";
    if (isPropertyScalarKind(declared)) {
      return declared;
    }
    return "string";
  };
  var editAllowed = (summary) => summary.definition?.allowed ? [...summary.definition.allowed] : [];
  var typedValueFrom = (value, kind, items) => {
    if (!value || !value.supported) {
      return void 0;
    }
    if (kind === "list") {
      const entries = value.items.length > 0 ? value.items : [value];
      return {
        kind: "list",
        text: "",
        items: entries.filter((entry) => entry.supported).map((entry) => ({ kind: items, text: entry.text }))
      };
    }
    return { kind, text: value.text };
  };
  var actionTitle = (action, property) => {
    switch (action) {
      case "set":
        return `Set ${property}`;
      case "unset":
        return `Remove ${property}`;
      case "replace_value":
        return `Replace a value of ${property}`;
      case "rename_property":
        return `Rename ${property}`;
      case "repair":
        return `Repair ${property}`;
    }
  };
  var actionConfirmLabel = (action) => {
    switch (action) {
      case "set":
        return "Apply values";
      case "unset":
        return "Remove property";
      case "replace_value":
        return "Replace values";
      case "rename_property":
        return "Rename property";
      case "repair":
        return "Repair notes";
    }
  };
  var selectionLabel = (count) => count === 0 ? "No notes selected." : `${count} ${count === 1 ? "note" : "notes"} selected.`;
  var mutationSuccessMessage = (result) => {
    const notes = `${result.affected} ${result.affected === 1 ? "note" : "notes"}`;
    const config = result.config_diff.length > 0 ? " and brainnotes.toml" : "";
    return result.affected === 0 && config === "" ? "Nothing changed." : `Updated ${notes}${config}.`;
  };
  var undoToastMessage = (result) => `${mutationSuccessMessage(result)} You can undo this change.`;

  // internal/web/static_src/properties-workbench-editor.ts
  var createWorkbenchEditor = (doc) => {
    let removeOpenEditor = null;
    const close = () => {
      const remove = removeOpenEditor;
      removeOpenEditor = null;
      remove?.();
    };
    const mount = (parent, title, submitLabel, build) => {
      close();
      const form = doc.createElement("form");
      form.className = "properties-editor";
      form.setAttribute("data-property-editor-form", "");
      const heading = doc.createElement("p");
      heading.className = "properties-editor-title";
      heading.textContent = title;
      form.appendChild(heading);
      const submit = build(form);
      const error = doc.createElement("p");
      error.className = "properties-editor-error";
      error.setAttribute("data-property-editor-error", "");
      error.setAttribute("role", "alert");
      error.hidden = true;
      form.appendChild(error);
      const actions = doc.createElement("div");
      actions.className = "properties-editor-actions";
      const confirm = doc.createElement("button");
      confirm.type = "submit";
      confirm.className = "properties-button properties-button-primary";
      confirm.setAttribute("data-property-editor-submit", "");
      confirm.textContent = submitLabel;
      const cancel = doc.createElement("button");
      cancel.type = "button";
      cancel.className = "properties-button";
      cancel.setAttribute("data-property-editor-cancel", "");
      cancel.textContent = "Cancel";
      actions.append(confirm, cancel);
      form.appendChild(actions);
      form.addEventListener("submit", (event) => {
        event.preventDefault();
        const message = submit();
        error.textContent = message || "";
        error.hidden = message === null || message === "";
      });
      form.addEventListener("keydown", (event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          close();
        }
      });
      cancel.addEventListener("click", close);
      parent.appendChild(form);
      removeOpenEditor = () => {
        form.remove();
      };
    };
    return {
      close,
      isOpen: () => removeOpenEditor !== null,
      mount,
      reset: () => {
        removeOpenEditor = null;
      }
    };
  };

  // internal/web/static_src/properties-workbench-mutations.ts
  var createWorkbenchMutations = (options) => {
    const announce = (outcome) => {
      if (!outcome.ok) {
        options.setStatus(outcome.message, "is-error");
        return false;
      }
      return true;
    };
    const finishWrite = async (result) => {
      options.setStatus(mutationSuccessMessage(result), "is-success");
      options.toast.show({
        message: undoToastMessage(result),
        journalID: result.journal_id,
        historyHref: options.historyHref
      });
      options.doc.defaultView?.dispatchEvent(
        new CustomEvent(managementMutationEventName, { detail: result })
      );
      await options.reload();
    };
    const runApply = async (previewID) => {
      options.setBusy(true);
      options.setStatus("Applying change...", "is-loading");
      try {
        const outcome = await applyPropertyMutation(options.fetcher, options.token, previewID);
        if (!announce(outcome)) {
          return;
        }
        options.clearSelection();
        await finishWrite(outcome.result);
      } finally {
        options.setBusy(false);
      }
    };
    const runPreview = async (request) => {
      options.setBusy(true);
      options.setStatus("Previewing change...", "is-loading");
      try {
        const outcome = await previewPropertyMutation(options.fetcher, options.token, request);
        if (!announce(outcome)) {
          return;
        }
        const { result } = outcome;
        options.setStatus(previewSummaryLabel(result));
        options.dialog.open({
          title: actionTitle(request.action, options.property),
          summary: previewSummaryLabel(result),
          confirmLabel: actionConfirmLabel(request.action),
          result,
          onConfirm: () => {
            void runApply(result.preview_id);
          },
          onCancel: () => {
            options.setStatus("Change canceled.");
          }
        });
      } finally {
        options.setBusy(false);
      }
    };
    const runSchemaApply = async (previewID) => {
      options.setBusy(true);
      options.setStatus("Saving the definition...", "is-loading");
      try {
        const outcome = await applyPropertySchema(options.fetcher, options.token, previewID);
        if (announce(outcome)) {
          await finishWrite(outcome.result);
        }
      } finally {
        options.setBusy(false);
      }
    };
    const previewSchema = async (op, definition) => {
      options.setBusy(true);
      options.setStatus("Previewing the definition...", "is-loading");
      try {
        const outcome = await previewPropertySchema(options.fetcher, options.token, {
          op,
          property: options.property,
          definition
        });
        if (!announce(outcome)) {
          return;
        }
        const { result } = outcome;
        options.dialog.open({
          title: op === "upsert" ? `Define ${options.property}` : `Remove the ${options.property} definition`,
          summary: previewSummaryLabel(result),
          confirmLabel: op === "upsert" ? "Save definition" : "Remove definition",
          result,
          onConfirm: () => {
            void runSchemaApply(result.preview_id);
          },
          onCancel: () => {
            options.setStatus("Definition change canceled.");
          }
        });
      } finally {
        options.setBusy(false);
      }
    };
    const runImmediate = async (relPath, op, value, hash, displayText) => {
      options.setBusy(true);
      options.setStatus("Saving...", "is-loading");
      try {
        const outcome = await savePropertyValue(options.fetcher, options.token, {
          note: relPath,
          property: options.property,
          op,
          expected_hash: hash,
          value
        });
        if (!announce(outcome)) {
          return;
        }
        options.closeEditor();
        options.updateRowText(relPath, displayText);
        await finishWrite(outcome.result);
      } finally {
        options.setBusy(false);
      }
    };
    const runUndo = async (journalID) => {
      options.setBusy(true);
      options.setStatus("Undoing change...", "is-loading");
      try {
        const outcome = await undoJournalEntry(options.fetcher, options.token, journalID);
        options.setStatus(outcome.message, outcome.ok ? "is-success" : "is-error");
        if (outcome.ok) {
          options.doc.defaultView?.dispatchEvent(new CustomEvent(managementMutationEventName));
          await options.reload();
        }
      } finally {
        options.setBusy(false);
      }
    };
    return { previewSchema, runImmediate, runPreview, runUndo };
  };

  // internal/web/static_src/properties-workbench.ts
  var createPropertyWorkbench = (host, deps = {}) => {
    const doc = host.root.ownerDocument;
    const fetcher = deps.fetch || fetch;
    const now = deps.now || (() => /* @__PURE__ */ new Date());
    const editorEl = queryElement(host.root, "[data-property-editor]", HTMLElement);
    const countEl = queryElement(host.root, "[data-property-selection-count]", HTMLElement);
    const selectAllEl = queryElement(host.root, "[data-property-select-all]", HTMLButtonElement);
    const clearSelectionEl = queryElement(
      host.root,
      "[data-property-clear-selection]",
      HTMLButtonElement
    );
    const searchEl = queryElement(host.root, "[data-property-filter-query]", HTMLInputElement);
    const dialogHost = queryElement(host.root, "[data-property-dialog-host]", HTMLElement) || host.root;
    const toastHost = queryElement(host.root, "[data-property-toast-host]", HTMLElement) || host.root;
    const dialog = createPreviewDialog(doc, dialogHost, { now });
    let runUndo = async (_journalID) => {
    };
    const toast = createUndoToast(doc, toastHost, {
      onUndo: (journalID) => {
        void runUndo(journalID);
      }
    });
    const editor = createWorkbenchEditor(doc);
    const selected = /* @__PURE__ */ new Set();
    let summary = null;
    let rows = [];
    let matchValue = null;
    let busy = false;
    const setStatus = (message, kind = "") => {
      host.setStatus(message, kind);
    };
    const rowElements = () => queryAllElements(host.rowsEl, "[data-property-row]", HTMLElement);
    const renderSelection = () => {
      if (countEl) {
        countEl.textContent = selectionLabel(selected.size);
      }
      if (clearSelectionEl) {
        clearSelectionEl.disabled = selected.size === 0;
      }
      for (const element2 of rowElements()) {
        const relPath = element2.getAttribute("data-property-row") || "";
        element2.classList.toggle("is-selected", selected.has(relPath));
      }
      for (const box of queryAllElements(
        host.rowsEl,
        "[data-property-row-select]",
        HTMLInputElement
      )) {
        box.checked = selected.has(box.getAttribute("data-property-row-select") || "");
      }
      renderActions();
    };
    const renderActions = () => {
      for (const button of queryAllElements(
        host.root,
        "[data-property-action]",
        HTMLButtonElement
      )) {
        const action = button.getAttribute("data-property-action") || "";
        const needsMatch = action === "replace_value";
        button.disabled = busy || selected.size === 0 || needsMatch && matchValue === null;
      }
    };
    const setBusy = (next) => {
      busy = next;
      host.root.setAttribute("aria-busy", next ? "true" : "false");
      renderActions();
    };
    const toggleSelection = (relPath, force) => {
      const next = force === void 0 ? !selected.has(relPath) : force;
      if (next) {
        selected.add(relPath);
      } else {
        selected.delete(relPath);
      }
      renderSelection();
    };
    const focusRow = (index) => {
      const elements = rowElements();
      if (elements.length === 0) {
        return;
      }
      const clamped = Math.max(0, Math.min(index, elements.length - 1));
      elements.forEach((element2, position) => {
        element2.tabIndex = position === clamped ? 0 : -1;
      });
      elements[clamped]?.focus();
    };
    const updateRowText = (relPath, text) => {
      const element2 = rowElements().find(
        (row) => row.getAttribute("data-property-row") === relPath
      );
      const value = element2 ? queryElement(element2, ".properties-row-value", HTMLElement) : null;
      if (!value) {
        return;
      }
      value.textContent = text;
      value.classList.remove("is-missing");
    };
    const mutations = createWorkbenchMutations({
      clearSelection: () => {
        selected.clear();
        renderSelection();
      },
      closeEditor: editor.close,
      dialog,
      doc,
      fetcher,
      historyHref: host.historyHref,
      property: host.property,
      reload: host.reload,
      setBusy,
      setStatus,
      toast,
      token: host.token,
      updateRowText
    });
    const { previewSchema, runImmediate, runPreview } = mutations;
    runUndo = mutations.runUndo;
    const buildValueControl = (form, current, seed) => {
      const control = createValueControl(doc, {
        kind: editKind(current),
        items: editItemKind(current),
        allowed: editAllowed(current),
        value: seed,
        id: `property-editor-${host.property}`,
        label: `${host.property} value`
      });
      form.appendChild(control.element);
      return control;
    };
    const openRowEditor = (relPath, op) => {
      const current = summary;
      const row = rows.find((candidate) => candidate.rel_path === relPath);
      const element2 = rowElements().find(
        (candidate) => candidate.getAttribute("data-property-row") === relPath
      );
      if (!row || !element2 || !current) {
        return;
      }
      const itemKind = editItemKind(current);
      const seed = op === "replace" ? typedValueFrom(row.value, editKind(current), itemKind) : void 0;
      editor.mount(
        element2,
        op === "replace" ? `Edit ${host.property}` : `Add to ${host.property}`,
        "Save",
        (form) => {
          const control = op === "replace" ? buildValueControl(form, current, seed) : (() => {
            const single = createValueControl(doc, {
              kind: itemKind,
              items: itemKind,
              allowed: editAllowed(current),
              id: `property-editor-item-${host.property}`,
              label: `${host.property} item`
            });
            form.appendChild(single.element);
            return single;
          })();
          control.focus();
          return () => {
            const message = control.error();
            if (message !== "") {
              return message;
            }
            const value = control.read();
            const text = op === "replace" ? previewTextFor(value) : `${valueDisplayText(row.value)}, ${value.text}`;
            void runImmediate(relPath, op, value, row.hash, text);
            return null;
          };
        }
      );
    };
    const previewTextFor = (value) => value.kind === "list" ? (value.items || []).map((item) => item.text).join(", ") : value.text;
    const openActionEditor = (action) => {
      const current = summary;
      const notes = [...selected];
      if (notes.length === 0) {
        setStatus("Select at least one note first.", "is-error");
        return;
      }
      if (action === "unset" || action === "repair") {
        void runPreview({ action, property: host.property, notes });
        return;
      }
      if (!editorEl || !current) {
        return;
      }
      editor.mount(editorEl, actionTitle(action, host.property), "Preview", (form) => {
        if (action === "rename_property") {
          const input = doc.createElement("input");
          input.type = "text";
          input.className = "properties-control properties-control-input";
          input.id = "property-editor-new-name";
          input.autocomplete = "off";
          input.spellcheck = false;
          input.setAttribute("aria-label", "New property name");
          input.setAttribute("data-property-new-name", "");
          input.value = host.property;
          form.appendChild(input);
          input.focus();
          return () => {
            const newName = input.value.trim();
            if (newName === "") {
              return "Enter a new property name.";
            }
            if (newName === host.property) {
              return "Enter a name different from the current one.";
            }
            void runPreview({
              action,
              property: host.property,
              new_name: newName,
              notes
            });
            return null;
          };
        }
        const control = buildValueControl(form, current);
        control.focus();
        return () => {
          const message = control.error();
          if (message !== "") {
            return message;
          }
          const value = control.read();
          if (action === "replace_value") {
            if (!matchValue) {
              return "Choose the value to replace first.";
            }
            void runPreview({
              action,
              property: host.property,
              notes,
              match: { kind: matchValue.kind, text: matchValue.text },
              value
            });
            return null;
          }
          void runPreview({ action, property: host.property, notes, value });
          return null;
        };
      });
    };
    host.rowsEl.addEventListener("change", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLInputElement) || !target.hasAttribute("data-property-row-select")) {
        return;
      }
      toggleSelection(target.getAttribute("data-property-row-select") || "", target.checked);
    });
    host.rowsEl.addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const edit = target.closest("[data-property-row-edit]");
      if (edit && !edit.disabled) {
        openRowEditor(edit.getAttribute("data-property-row-edit") || "", "replace");
        return;
      }
      const add = target.closest("[data-property-row-add]");
      if (add && !add.disabled) {
        openRowEditor(add.getAttribute("data-property-row-add") || "", "list_add");
        return;
      }
      const unset = target.closest("[data-property-row-unset]");
      if (unset && !unset.disabled) {
        void runPreview({
          action: "unset",
          property: host.property,
          notes: [unset.getAttribute("data-property-row-unset") || ""]
        });
      }
    });
    host.rowsEl.addEventListener("keydown", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) {
        return;
      }
      const row = target.closest("[data-property-row]");
      if (!row) {
        return;
      }
      const elements = rowElements();
      const index = elements.indexOf(row);
      if (event.key === "ArrowDown") {
        event.preventDefault();
        focusRow(index + 1);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        focusRow(index - 1);
        return;
      }
      if (target !== row) {
        return;
      }
      if (event.key === " ") {
        event.preventDefault();
        toggleSelection(row.getAttribute("data-property-row") || "");
        return;
      }
      if (event.key === "Enter") {
        event.preventDefault();
        openRowEditor(row.getAttribute("data-property-row") || "", "replace");
      }
    });
    host.root.addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const button = target.closest("[data-property-action]");
      if (!button || button.disabled) {
        return;
      }
      openActionEditor(button.getAttribute("data-property-action"));
    });
    selectAllEl?.addEventListener("click", () => {
      for (const row of rows) {
        selected.add(row.rel_path);
      }
      renderSelection();
    });
    clearSelectionEl?.addEventListener("click", () => {
      selected.clear();
      renderSelection();
    });
    doc.addEventListener("keydown", (event) => {
      if (dialog.isOpen()) {
        return;
      }
      if (event.key === "/" && searchEl) {
        const target = event.target;
        const typing = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement;
        if (typing) {
          return;
        }
        event.preventDefault();
        searchEl.focus();
        searchEl.select();
        return;
      }
      if (event.key === "Escape") {
        if (editor.isOpen()) {
          editor.close();
          return;
        }
        if (selected.size > 0) {
          selected.clear();
          renderSelection();
          setStatus("Selection cleared.");
        }
      }
    });
    return {
      render: (result) => {
        summary = result.property;
        rows = result.rows;
        const active = result.filter.value || "";
        const match = result.values.find(
          (entry) => entry.value.supported && entry.value.text === active
        );
        matchValue = match ? { kind: match.value.kind, text: match.value.text } : null;
        editor.reset();
        renderRows(host.rowsEl, result.rows, {
          selected,
          listKind: editKind(result.property) === "list"
        });
        const first = rowElements()[0];
        if (first) {
          first.tabIndex = 0;
        }
        renderSelection();
      },
      selection: () => [...selected],
      previewSchema
    };
  };

  // internal/web/static_src/properties-detail.ts
  var readPayload3 = async (resp) => {
    try {
      return await resp.json();
    } catch {
      return null;
    }
  };
  var initPropertyDetail = (deps = {}) => {
    const root = queryElement(document, "[data-property-detail]", HTMLElement);
    if (!root) {
      return;
    }
    const statusEl = queryElement(root, "[data-property-status]", HTMLElement);
    const rowsEl = queryElement(root, "[data-property-rows]", HTMLElement);
    if (!statusEl || !rowsEl) {
      return;
    }
    const configEl = queryElement(root, "[data-property-config]", HTMLElement);
    const definitionEl = queryElement(root, "[data-property-definition]", HTMLElement);
    const summaryEl = queryElement(root, "[data-property-summary]", HTMLElement);
    const proposalEl = queryElement(root, "[data-property-proposal]", HTMLElement);
    const valuesEl = queryElement(root, "[data-property-values]", HTMLElement);
    const statusFilterEl = queryElement(root, "[data-property-filter-status]", HTMLSelectElement);
    const queryEl = queryElement(root, "[data-property-filter-query]", HTMLInputElement);
    const clearValueEl = queryElement(root, "[data-property-filter-clear]", HTMLButtonElement);
    const activeValueEl = queryElement(root, "[data-property-active-value]", HTMLElement);
    const paginationEl = queryElement(root, "[data-property-pagination]", HTMLElement);
    const prevEl = queryElement(root, "[data-property-prev]", HTMLButtonElement);
    const nextEl = queryElement(root, "[data-property-next]", HTMLButtonElement);
    const refreshEl = queryElement(root, "[data-property-refresh]", HTMLButtonElement);
    const config = readPropertyDetailConfig(root);
    if (config.property === "") {
      return;
    }
    const fetcher = deps.fetch || fetch;
    const filter = emptyDetailFilter();
    const invalidEl = queryElement(root, "[data-property-config-invalid]", HTMLElement);
    const restartEl = queryElement(root, "[data-property-config-restart]", HTMLElement);
    const setStatus = (message, kind = "") => {
      setStatusText(statusEl, message, kind);
    };
    const renderBanners = (status) => {
      if (invalidEl) {
        const message = configInvalidMessage(status);
        invalidEl.textContent = message;
        invalidEl.hidden = message === "";
      }
      if (restartEl) {
        const message = configRestartMessage(status);
        restartEl.textContent = message;
        restartEl.hidden = message === "";
      }
    };
    const workbench = config.token === "" ? null : createPropertyWorkbench(
      {
        root,
        rowsEl,
        property: config.property,
        token: config.token,
        historyHref: config.historyPath,
        reload: () => load(),
        setStatus
      },
      deps
    );
    const schemaEditor = workbench ? createPropertySchemaEditor({
      root,
      submit: (op, definition) => {
        void workbench.previewSchema(op, definition);
      },
      setStatus: (message) => {
        setStatus(message);
      }
    }) : null;
    const renderActiveValue = () => {
      if (activeValueEl) {
        activeValueEl.textContent = filter.value === "" ? "" : `Filtered to the value "${filter.value}".`;
        activeValueEl.hidden = filter.value === "";
      }
      if (clearValueEl) {
        clearValueEl.hidden = filter.value === "";
      }
    };
    const load = async () => {
      root.setAttribute("aria-busy", "true");
      setStatus("Loading property...", "is-loading");
      try {
        const url = buildPropertyDetailURL(config.detailEndpoint, {
          property: config.property,
          status: filter.status,
          value: filter.value,
          q: filter.query,
          limit: PROPERTY_ROW_LIMIT,
          offset: filter.offset
        });
        const resp = await fetcher(url, { headers: { Accept: "application/json" } });
        const payload = await readPayload3(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = normalizePropertyDetailResponse(payload);
        if (!result.ok) {
          setStatus(result.message || "Property unavailable.", "is-error");
          return;
        }
        renderSummary(summaryEl, result.property);
        renderDefinition(definitionEl, result.property);
        renderProposal(proposalEl, result.property);
        renderValues(valuesEl, result.values, filter.value);
        if (workbench) {
          workbench.render(result);
        } else {
          renderRows(rowsEl, result.rows);
        }
        schemaEditor?.sync(result.property);
        renderPagination(paginationEl, result.pagination);
        renderActiveValue();
        renderBanners(result.config);
        if (configEl) {
          const message = configStatusMessage(result.config);
          configEl.textContent = message;
          configEl.hidden = message === "";
        }
        if (prevEl) {
          prevEl.disabled = result.pagination.offset === 0;
        }
        if (nextEl) {
          nextEl.disabled = !result.pagination.has_more;
        }
        filter.offset = result.pagination.offset;
        setStatus(
          detailStatusMessage(result),
          result.pagination.total === 0 ? "is-empty" : ""
        );
        if (nextEl && result.pagination.has_more) {
          nextEl.setAttribute("data-property-next-offset", String(nextOffset(result.pagination)));
        }
        if (prevEl) {
          prevEl.setAttribute(
            "data-property-prev-offset",
            String(previousOffset(result.pagination, result.pagination.limit))
          );
        }
      } catch {
        setStatus("Property unavailable.", "is-error");
      } finally {
        root.setAttribute("aria-busy", "false");
      }
    };
    statusFilterEl?.addEventListener("change", () => {
      filter.status = normalizePropertyRowFilterStatus(statusFilterEl.value);
      filter.offset = 0;
      void load();
    });
    queryEl?.addEventListener("change", () => {
      filter.query = queryEl.value;
      filter.offset = 0;
      void load();
    });
    clearValueEl?.addEventListener("click", () => {
      filter.value = "";
      filter.offset = 0;
      void load();
    });
    refreshEl?.addEventListener("click", () => {
      void load();
    });
    prevEl?.addEventListener("click", () => {
      filter.offset = Number(prevEl.getAttribute("data-property-prev-offset") || "0");
      void load();
    });
    nextEl?.addEventListener("click", () => {
      filter.offset = Number(nextEl.getAttribute("data-property-next-offset") || "0");
      void load();
    });
    valuesEl?.addEventListener("click", (event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const button = target.closest("[data-property-value]");
      if (!button || !valuesEl.contains(button) || button.disabled) {
        return;
      }
      const value = button.getAttribute("data-property-value") || "";
      filter.value = filter.value === value ? "" : value;
      filter.offset = 0;
      void load();
    });
    void load();
    if (config.token !== "") {
      createConfigStatusPoller(document, {
        fetch: fetcher,
        onStatus: renderBanners,
        onGenerationChange: () => {
          setStatus("brainnotes.toml changed on disk. Reloading...", "is-loading");
          void load();
        }
      }).start();
    }
  };

  // internal/web/static_src/properties.ts
  var initProperties = (deps = {}) => {
    initPropertiesCatalog(deps);
    initPropertyDetail(deps);
  };

  // internal/web/static_src/related-types.ts
  var normalizeIdentity = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const identity = {
      key: normalizeParamValue(record.key),
      title: normalizeParamValue(record.title),
      path: normalizeParamValue(record.path),
      route: normalizeParamValue(record.route)
    };
    return identity.key !== "" && identity.path !== "" && identity.route !== "" ? identity : null;
  };

  // internal/web/static_src/related-api.ts
  var relatedModes = ["balanced", "rediscover", "connect"];
  var reasonKinds = ["text", "graph", "metadata", "recall"];
  var feedbackStates = ["useful", "dismissed", "later"];
  var normalizeRelatedMode = (value) => {
    const mode = normalizeParamValue(value).toLowerCase();
    return relatedModes.includes(mode) ? mode : null;
  };
  var normalizeReason = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const kind = normalizeParamValue(record.kind).toLowerCase();
    const detail = normalizeParamValue(record.detail);
    if (!reasonKinds.includes(kind) || detail === "") {
      return null;
    }
    return { kind, detail };
  };
  var normalizeRelationship = (value) => {
    switch (normalizeParamValue(value).toLowerCase()) {
      case "direct":
        return "direct";
      case "shared-neighbor":
        return "shared-neighbor";
      case "two-hop":
        return "two-hop";
      default:
        return "";
    }
  };
  var normalizeResult = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const candidate = normalizeIdentity(record.candidate);
    if (!candidate) {
      return null;
    }
    const bridge = normalizeIdentity(record.bridge);
    const reasons = Array.isArray(record.reasons) ? record.reasons.flatMap((reason) => {
      const normalized = normalizeReason(reason);
      return normalized ? [normalized] : [];
    }).slice(0, 3) : [];
    return {
      candidate,
      excerpt: normalizeParamValue(record.excerpt),
      relationship: normalizeRelationship(record.relationship),
      bridge: bridge || void 0,
      reasons,
      useful: readBoolean(record.useful)
    };
  };
  var normalizeFeedback = (value) => {
    const record = asRecord(value);
    if (!record) {
      return null;
    }
    const candidate = normalizeIdentity(record.candidate);
    const state = normalizeParamValue(record.state).toLowerCase();
    if (!candidate || !feedbackStates.includes(state)) {
      return null;
    }
    const until = normalizeParamValue(record.until);
    return {
      candidate,
      state,
      until: state === "later" && until !== "" ? until : void 0
    };
  };
  var normalizeRelatedResponse = (payload) => {
    const record = asRecord(payload);
    if (!record) {
      return null;
    }
    const source = normalizeIdentity(record.source);
    const mode = normalizeRelatedMode(record.mode);
    if (!source || !mode) {
      return null;
    }
    const results = Array.isArray(record.results) ? record.results.flatMap((result) => {
      const normalized = normalizeResult(result);
      return normalized ? [normalized] : [];
    }) : [];
    const feedback = Array.isArray(record.feedback) ? record.feedback.flatMap((entry) => {
      const normalized = normalizeFeedback(entry);
      return normalized ? [normalized] : [];
    }) : [];
    return {
      serverDate: normalizeParamValue(record.server_date),
      source,
      mode,
      hasMore: readBoolean(record.has_more),
      results,
      feedback
    };
  };
  var buildRelatedRequestURL = (endpoint, source, mode, limit) => {
    const params = new URLSearchParams();
    params.set("source", source);
    params.set("mode", mode);
    params.set("limit", String(Math.max(1, Math.min(20, Math.trunc(limit) || 1))));
    const separator = endpoint.includes("?") ? "&" : "?";
    return `${endpoint}${separator}${params.toString()}`;
  };
  var responseErrorMessage = (payload, status) => {
    const record = asRecord(payload);
    const message = record ? normalizeParamValue(record.message) : "";
    return message || `Related notes request failed (${status}).`;
  };
  var requestRelated = async (fetcher, endpoint, source, mode, limit) => {
    const response = await fetcher(buildRelatedRequestURL(endpoint, source, mode, limit), {
      headers: { Accept: "application/json" }
    });
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw new Error(responseErrorMessage(payload, response.status));
    }
    const normalized = normalizeRelatedResponse(payload);
    if (!normalized || normalized.mode !== mode || normalized.source.key !== source) {
      throw new Error("Related notes returned an invalid response.");
    }
    return normalized;
  };

  // internal/web/static_src/related-actions.ts
  var itemActions = [
    "view",
    "open_editor",
    "useful",
    "dismissed",
    "later",
    "clear"
  ];
  var feedbackStates2 = ["useful", "dismissed", "later"];
  var buildRelatedItemForm = (request) => {
    const body = new URLSearchParams();
    body.set("source", request.source);
    body.set("candidate", request.candidate);
    body.set("action", request.action);
    if (request.until) {
      body.set("until", request.until);
    }
    body.set("token", request.token);
    return body;
  };
  var buildRelatedItemRequest = (request, keepalive = false) => ({
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": request.token
    },
    body: buildRelatedItemForm(request).toString(),
    ...keepalive ? { keepalive: true } : {}
  });
  var normalizeRelatedItemResponse = (payload) => {
    const record = asRecord(payload);
    if (!record || !readBoolean(record.ok)) {
      return null;
    }
    const action = normalizeParamValue(record.action).toLowerCase();
    const source = normalizeIdentity(record.source);
    const candidate = normalizeIdentity(record.candidate);
    if (!itemActions.includes(action) || !source || !candidate) {
      return null;
    }
    const stateValue = normalizeParamValue(record.state).toLowerCase();
    const state = feedbackStates2.includes(stateValue) || stateValue === "clear" ? stateValue : void 0;
    return {
      ok: true,
      action,
      source,
      candidate,
      state,
      until: normalizeParamValue(record.until) || void 0,
      opened: readBoolean(record.opened),
      recorded: typeof record.recorded === "boolean" ? record.recorded : void 0,
      warning: normalizeParamValue(record.warning) || void 0
    };
  };
  var itemErrorMessage = (payload, status) => {
    const record = asRecord(payload);
    return record && normalizeParamValue(record.message) || `Related action failed (${status}).`;
  };
  var requestRelatedItem = async (fetcher, endpoint, request) => {
    const response = await fetcher(endpoint, buildRelatedItemRequest(request));
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw new Error(itemErrorMessage(payload, response.status));
    }
    const result = normalizeRelatedItemResponse(payload);
    if (!result || result.action !== request.action || result.source.key !== request.source || result.candidate.key !== request.candidate) {
      throw new Error("Related action returned an invalid response.");
    }
    return result;
  };
  var trackRelatedView = (fetcher, beacon, endpoint, request) => {
    const viewRequest = { ...request, action: "view" };
    if (beacon) {
      try {
        const body = new Blob([buildRelatedItemForm(viewRequest).toString()], {
          type: "application/x-www-form-urlencoded; charset=UTF-8"
        });
        if (beacon(endpoint, body)) {
          return;
        }
      } catch {
      }
    }
    void fetcher(endpoint, buildRelatedItemRequest(viewRequest, true)).catch(() => void 0);
  };

  // internal/web/static_src/related-model.ts
  var RELATED_INITIAL_LIMIT = 5;
  var RELATED_LIMIT_STEP = 5;
  var RELATED_MAX_LIMIT = 20;
  var relatedModeDefinitions = [
    {
      mode: "balanced",
      label: "Balanced",
      description: "A broad mix of the strongest connections."
    },
    {
      mode: "rediscover",
      label: "Rediscover",
      description: "Older notes that may be worth revisiting."
    },
    {
      mode: "connect",
      label: "Connect",
      description: "Notes connected through shared context."
    }
  ];
  var createModeState = () => ({
    limit: RELATED_INITIAL_LIMIT,
    loaded: false,
    loading: false,
    error: "",
    stale: false
  });
  var createRelatedModel = () => ({
    activeMode: "balanced",
    modes: {
      balanced: createModeState(),
      rediscover: createModeState(),
      connect: createModeState()
    },
    feedback: [],
    serverDate: ""
  });
  var nextRelatedLimit = (limit) => Math.min(RELATED_MAX_LIMIT, limit + RELATED_LIMIT_STEP);
  var parseCalendarDate = (value) => {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
    if (!match) {
      return null;
    }
    const year = Number(match[1]);
    const month = Number(match[2]);
    const day = Number(match[3]);
    const date = new Date(Date.UTC(year, month - 1, day));
    if (date.getUTCFullYear() !== year || date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) {
      return null;
    }
    return [year, month, day];
  };
  var formatCalendarDate = (date) => `${date.getUTCFullYear().toString().padStart(4, "0")}-${(date.getUTCMonth() + 1).toString().padStart(2, "0")}-${date.getUTCDate().toString().padStart(2, "0")}`;
  var addRelatedCalendarDays = (serverDate, days) => {
    const parsed = parseCalendarDate(serverDate);
    if (!parsed) {
      return "";
    }
    const [year, month, day] = parsed;
    const date = new Date(Date.UTC(year, month - 1, day));
    date.setUTCDate(date.getUTCDate() + Math.trunc(days));
    return formatCalendarDate(date);
  };
  var validateRelatedLaterDate = (serverDate, value) => {
    const parsed = parseCalendarDate(value);
    const minimum = addRelatedCalendarDays(serverDate, 1);
    if (!parsed || minimum === "" || value < minimum) {
      return "Choose a valid date after the server date.";
    }
    return "";
  };
  var relatedRelationshipLabel = (relationship) => {
    switch (relationship) {
      case "direct":
        return "Directly linked";
      case "shared-neighbor":
        return "Shared neighbor";
      case "two-hop":
        return "Two links away";
      default:
        return "Related by content or metadata";
    }
  };
  var relatedReasonLabel = (kind) => {
    switch (kind) {
      case "text":
        return "Text match";
      case "graph":
        return "Link structure";
      case "metadata":
        return "Shared metadata";
      case "recall":
        return "Recall history";
      default:
        return "Reason";
    }
  };

  // internal/web/static_src/related-render.ts
  var relatedID = (kind, mode) => `related-${kind}-${mode}`;
  var createModePanel = (doc, mode, description) => {
    const panel = doc.createElement("section");
    panel.id = relatedID("panel", mode);
    panel.className = "related-mode-panel";
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", relatedID("tab", mode));
    panel.setAttribute("data-related-panel", mode);
    panel.tabIndex = 0;
    panel.hidden = mode !== "balanced";
    const descriptionEl = doc.createElement("p");
    descriptionEl.className = "related-mode-description";
    descriptionEl.textContent = description;
    const results = doc.createElement("div");
    results.className = "related-results";
    results.setAttribute("data-related-results", mode);
    const more = doc.createElement("button");
    more.type = "button";
    more.className = "related-show-more";
    more.setAttribute("data-related-show-more", mode);
    more.textContent = "Show more";
    more.hidden = true;
    panel.append(descriptionEl, results, more);
    return { panel, results, more };
  };
  var createRelatedView = (root) => {
    const doc = root.ownerDocument;
    const heading = root.querySelector("h2");
    const status = root.querySelector("[data-related-status]") || doc.createElement("p");
    status.className = "related-status";
    status.setAttribute("data-related-status", "");
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    status.tabIndex = -1;
    const intro = doc.createElement("p");
    intro.className = "related-notes-intro";
    intro.textContent = "Explore nearby ideas without changing this note.";
    const tablist = doc.createElement("div");
    tablist.className = "related-tab-list";
    tablist.setAttribute("role", "tablist");
    tablist.setAttribute("aria-label", "Related note modes");
    const feedbackDrawer = doc.createElement("details");
    feedbackDrawer.className = "related-feedback-drawer";
    feedbackDrawer.setAttribute("data-related-feedback-drawer", "");
    const feedbackSummary = doc.createElement("summary");
    feedbackSummary.setAttribute("data-related-feedback-summary", "");
    feedbackSummary.textContent = "Feedback (0)";
    const feedbackList = doc.createElement("div");
    feedbackList.className = "related-feedback-list";
    feedbackList.setAttribute("data-related-feedback-list", "");
    const feedbackEmpty = doc.createElement("p");
    feedbackEmpty.className = "related-feedback-empty";
    feedbackEmpty.textContent = "No feedback yet.";
    feedbackList.appendChild(feedbackEmpty);
    feedbackDrawer.append(feedbackSummary, feedbackList);
    const panelsContainer = doc.createElement("div");
    panelsContainer.className = "related-panels";
    const tabs = {};
    const panels = {};
    const results = {};
    const moreButtons = {};
    for (const definition of relatedModeDefinitions) {
      const tab = doc.createElement("button");
      tab.type = "button";
      tab.id = relatedID("tab", definition.mode);
      tab.className = "related-tab";
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-controls", relatedID("panel", definition.mode));
      tab.setAttribute("aria-selected", definition.mode === "balanced" ? "true" : "false");
      tab.setAttribute("data-related-tab", definition.mode);
      tab.tabIndex = definition.mode === "balanced" ? 0 : -1;
      const label = doc.createElement("span");
      label.className = "related-tab-label";
      label.textContent = definition.label;
      const description = doc.createElement("span");
      description.className = "related-tab-description";
      description.textContent = definition.description;
      tab.append(label, description);
      tablist.appendChild(tab);
      const modePanel = createModePanel(doc, definition.mode, definition.description);
      panelsContainer.appendChild(modePanel.panel);
      tabs[definition.mode] = tab;
      panels[definition.mode] = modePanel.panel;
      results[definition.mode] = modePanel.results;
      moreButtons[definition.mode] = modePanel.more;
    }
    root.replaceChildren();
    if (heading) {
      root.appendChild(heading);
    } else {
      const fallbackHeading = doc.createElement("h2");
      fallbackHeading.textContent = "Related Notes";
      root.appendChild(fallbackHeading);
    }
    root.append(intro, tablist, status, feedbackDrawer, panelsContainer);
    return { status, tabs, panels, results, moreButtons, feedbackDrawer, feedbackSummary, feedbackList };
  };
  var selectRelatedModeView = (view, activeMode) => {
    for (const definition of relatedModeDefinitions) {
      const selected = definition.mode === activeMode;
      view.tabs[definition.mode].setAttribute("aria-selected", selected ? "true" : "false");
      view.tabs[definition.mode].tabIndex = selected ? 0 : -1;
      view.panels[definition.mode].hidden = !selected;
    }
  };
  var actionButton = (doc, label, action, candidate) => {
    const button = doc.createElement("button");
    button.type = "button";
    button.className = "related-card-action";
    button.textContent = label;
    button.setAttribute("data-related-action", action);
    button.setAttribute("data-related-candidate", candidate);
    return button;
  };
  var renderLaterControl = (doc, result, serverDate) => {
    const details = doc.createElement("details");
    details.className = "related-later-control";
    details.setAttribute("data-related-later-control", result.candidate.key);
    const summary = doc.createElement("summary");
    summary.textContent = "Later";
    const options = doc.createElement("div");
    options.className = "related-later-options";
    const tomorrow = actionButton(doc, "Tomorrow", "later", result.candidate.key);
    tomorrow.setAttribute("data-related-until", addRelatedCalendarDays(serverDate, 1));
    const nextWeek = actionButton(doc, "Next week", "later", result.candidate.key);
    nextWeek.setAttribute("data-related-until", addRelatedCalendarDays(serverDate, 7));
    const form = doc.createElement("form");
    form.className = "related-later-custom";
    form.setAttribute("data-related-later-form", result.candidate.key);
    const label = doc.createElement("label");
    label.textContent = "Custom date";
    const input = doc.createElement("input");
    input.type = "date";
    input.name = "until";
    input.required = true;
    input.min = addRelatedCalendarDays(serverDate, 1);
    input.setAttribute("data-related-later-date", "");
    label.appendChild(input);
    const submit = doc.createElement("button");
    submit.type = "submit";
    submit.className = "related-card-action";
    submit.textContent = "Choose date";
    form.append(label, submit);
    options.append(tomorrow, nextWeek, form);
    details.append(summary, options);
    return details;
  };
  var renderRelatedCard = (doc, result, serverDate) => {
    const card = doc.createElement("article");
    card.className = "related-card";
    card.setAttribute("data-related-card", result.candidate.key);
    card.tabIndex = -1;
    const heading = doc.createElement("h3");
    heading.className = "related-card-title";
    const link = doc.createElement("a");
    link.href = result.candidate.route;
    link.textContent = result.candidate.title || result.candidate.path;
    link.setAttribute("data-related-view-candidate", result.candidate.key);
    heading.appendChild(link);
    const path = doc.createElement("p");
    path.className = "related-card-path";
    const pathCode = doc.createElement("code");
    pathCode.textContent = result.candidate.path;
    path.appendChild(pathCode);
    const relationship = doc.createElement("p");
    relationship.className = "related-card-relationship";
    relationship.textContent = relatedRelationshipLabel(result.relationship);
    card.append(heading, path, relationship);
    if (result.bridge) {
      const bridge = doc.createElement("p");
      bridge.className = "related-card-bridge";
      const bridgeLink = doc.createElement("a");
      bridgeLink.href = result.bridge.route;
      bridgeLink.textContent = result.bridge.title || result.bridge.path;
      bridgeLink.setAttribute("data-related-view-candidate", result.bridge.key);
      bridge.append("Via ", bridgeLink);
      card.appendChild(bridge);
    }
    if (result.excerpt !== "") {
      const excerpt = doc.createElement("blockquote");
      excerpt.className = "related-card-excerpt";
      excerpt.textContent = result.excerpt;
      card.appendChild(excerpt);
    }
    if (result.reasons.length > 0) {
      const reasons = doc.createElement("ul");
      reasons.className = "related-card-reasons";
      for (const reason of result.reasons.slice(0, 3)) {
        const item = doc.createElement("li");
        const label = doc.createElement("strong");
        label.textContent = `${relatedReasonLabel(reason.kind)}: `;
        const detail = doc.createElement("span");
        detail.textContent = reason.detail;
        item.append(label, detail);
        reasons.appendChild(item);
      }
      card.appendChild(reasons);
    }
    const actions = doc.createElement("div");
    actions.className = "related-card-actions";
    const useful = actionButton(doc, "Useful", "useful", result.candidate.key);
    useful.setAttribute("aria-pressed", result.useful ? "true" : "false");
    actions.append(
      useful,
      actionButton(doc, "Dismiss", "dismissed", result.candidate.key),
      renderLaterControl(doc, result, serverDate),
      actionButton(doc, "Open in editor", "open_editor", result.candidate.key)
    );
    card.appendChild(actions);
    return card;
  };
  var renderRelatedFeedback = (view, feedback) => {
    view.feedbackSummary.textContent = `Feedback (${feedback.length})`;
    view.feedbackList.replaceChildren();
    if (feedback.length === 0) {
      const empty = view.feedbackList.ownerDocument.createElement("p");
      empty.className = "related-feedback-empty";
      empty.textContent = "No feedback yet.";
      view.feedbackList.appendChild(empty);
      return;
    }
    for (const entry of feedback) {
      const row = view.feedbackList.ownerDocument.createElement("div");
      row.className = "related-feedback-row";
      row.setAttribute("data-related-feedback-row", entry.candidate.key);
      const link = view.feedbackList.ownerDocument.createElement("a");
      link.href = entry.candidate.route;
      link.textContent = entry.candidate.title || entry.candidate.path;
      link.setAttribute("data-related-view-candidate", entry.candidate.key);
      const state = view.feedbackList.ownerDocument.createElement("span");
      state.className = "related-feedback-state";
      state.textContent = entry.state === "dismissed" ? "Dismissed" : entry.state === "later" ? "Later" : "Useful";
      if (entry.state === "later" && entry.until) {
        const deadline = view.feedbackList.ownerDocument.createElement("time");
        deadline.dateTime = entry.until;
        deadline.textContent = ` until ${entry.until}`;
        state.appendChild(deadline);
      }
      const clear = actionButton(view.feedbackList.ownerDocument, "Clear", "clear", entry.candidate.key);
      clear.classList.add("related-feedback-clear");
      row.append(link, state, clear);
      view.feedbackList.appendChild(row);
    }
  };
  var renderRelatedResponse = (view, response, requestedLimit) => {
    const container = view.results[response.mode];
    container.replaceChildren();
    if (response.results.length === 0) {
      const empty = container.ownerDocument.createElement("p");
      empty.className = "related-empty";
      empty.textContent = "No related notes are available in this mode.";
      container.appendChild(empty);
    } else {
      for (const result of response.results) {
        container.appendChild(renderRelatedCard(container.ownerDocument, result, response.serverDate));
      }
    }
    const more = view.moreButtons[response.mode];
    more.hidden = !response.hasMore || requestedLimit >= RELATED_MAX_LIMIT;
    more.disabled = false;
  };
  var setRelatedModeBusy = (view, mode, busy) => {
    view.panels[mode].setAttribute("aria-busy", busy ? "true" : "false");
    view.moreButtons[mode].disabled = busy;
  };
  var setRelatedCardBusy = (card, busy) => {
    card.setAttribute("aria-busy", busy ? "true" : "false");
    for (const control of card.querySelectorAll("button, input")) {
      control.disabled = busy;
    }
  };
  var setRelatedFeedbackBusy = (row, busy) => {
    row.setAttribute("aria-busy", busy ? "true" : "false");
    for (const button of row.querySelectorAll("button")) {
      button.disabled = busy;
    }
  };

  // internal/web/static_src/related-controller.ts
  var readRelatedConfig = (root) => ({
    source: (root.getAttribute("data-related-source-key") || "").trim(),
    endpoint: root.getAttribute("data-related-endpoint") || "/api/related",
    itemEndpoint: root.getAttribute("data-related-item-endpoint") || "/api/related/item",
    csrfToken: root.getAttribute("data-related-csrf") || ""
  });
  var errorMessage = (error) => error instanceof Error && error.message !== "" ? error.message : "Related notes could not be loaded.";
  var statusForMode = (model, mode) => {
    const state = model.modes[mode];
    if (state.loading) {
      return [`Loading ${mode} related notes\u2026`, "is-loading"];
    }
    if (state.error !== "") {
      return [state.error, "is-error"];
    }
    if (state.response) {
      if (state.response.results.length === 0) {
        return ["No related notes are available in this mode.", "is-empty"];
      }
      const count = state.response.results.length;
      return [`Showing ${count} related ${count === 1 ? "note" : "notes"}.`, "is-success"];
    }
    return ["Select a mode to load related notes.", ""];
  };
  var actionFromValue = (value) => {
    switch (value) {
      case "open_editor":
      case "useful":
      case "dismissed":
      case "later":
      case "clear":
        return value;
      default:
        return null;
    }
  };
  var cardForCandidate = (view, mode, candidate) => {
    for (const card of view.results[mode].querySelectorAll("[data-related-card]")) {
      if (card.getAttribute("data-related-card") === candidate) {
        return card;
      }
    }
    return null;
  };
  var createRelatedController = (root, deps = {}) => {
    const config = readRelatedConfig(root);
    const model = createRelatedModel();
    const view = createRelatedView(root);
    const request = deps.fetch || fetch;
    const fetcher = (input, init) => request(input, init);
    const beacon = deps.sendBeacon || (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function" ? navigator.sendBeacon.bind(navigator) : void 0);
    const requestIDs = { balanced: 0, rediscover: 0, connect: 0 };
    const loadPromises = {
      balanced: null,
      rediscover: null,
      connect: null
    };
    const setStatus = (message, kind = "") => {
      setStatusText(view.status, message, kind);
    };
    const showStatus = (mode) => {
      if (model.activeMode !== mode) {
        return;
      }
      const [message, kind] = statusForMode(model, mode);
      setStatus(message, kind);
    };
    const load = (mode, requestedLimit) => {
      const state = model.modes[mode];
      if (state.loading) {
        return Promise.resolve(false);
      }
      state.loading = true;
      state.error = "";
      const requestID = requestIDs[mode] + 1;
      requestIDs[mode] = requestID;
      setRelatedModeBusy(view, mode, true);
      showStatus(mode);
      const loadPromise = (async () => {
        let succeeded = false;
        try {
          const response = await requestRelated(fetcher, config.endpoint, config.source, mode, requestedLimit);
          if (requestIDs[mode] !== requestID) {
            return false;
          }
          state.limit = requestedLimit;
          state.loaded = true;
          state.stale = false;
          state.response = response;
          model.feedback = response.feedback;
          model.serverDate = response.serverDate;
          renderRelatedResponse(view, response, requestedLimit);
          renderRelatedFeedback(view, model.feedback);
          succeeded = true;
        } catch (error) {
          if (requestIDs[mode] !== requestID) {
            return false;
          }
          state.error = errorMessage(error);
        } finally {
          if (requestIDs[mode] === requestID) {
            state.loading = false;
            setRelatedModeBusy(view, mode, false);
            showStatus(mode);
          }
        }
        return succeeded;
      })();
      loadPromises[mode] = loadPromise;
      const clearLoadPromise = () => {
        if (loadPromises[mode] === loadPromise) {
          loadPromises[mode] = null;
        }
      };
      void loadPromise.then(clearLoadPromise, clearLoadPromise);
      return loadPromise;
    };
    const refreshAfterMutation = async (mode) => {
      const pendingLoad = loadPromises[mode];
      if (pendingLoad) {
        await pendingLoad;
      }
      return load(mode, model.modes[mode].limit);
    };
    const selectMode = (mode) => {
      model.activeMode = mode;
      selectRelatedModeView(view, mode);
      showStatus(mode);
      const state = model.modes[mode];
      if ((!state.loaded || state.stale) && !state.loading) {
        void load(mode, state.limit);
      }
    };
    const markInactiveModesStale = () => {
      for (const definition of relatedModeDefinitions) {
        if (definition.mode !== model.activeMode) {
          model.modes[definition.mode].stale = true;
        }
      }
    };
    const focusAfterMutation = (candidate, nextCandidate, previousCandidate, fromDrawer) => {
      const preferred = fromDrawer ? [candidate] : [candidate, nextCandidate, previousCandidate];
      for (const key of preferred) {
        if (key === "") {
          continue;
        }
        const card = cardForCandidate(view, model.activeMode, key);
        if (card) {
          card.focus({ preventScroll: true });
          return;
        }
      }
      (fromDrawer ? view.feedbackSummary : view.tabs[model.activeMode]).focus({ preventScroll: true });
    };
    const runMutation = async (action, candidate, until, origin) => {
      if (config.source === "" || config.csrfToken === "" || candidate === "") {
        setStatus("Related action unavailable.", "is-error");
        return;
      }
      if (action === "later") {
        const validationError = validateRelatedLaterDate(model.serverDate, until);
        if (validationError) {
          setStatus(validationError, "is-error");
          return;
        }
      }
      const card = origin.closest("[data-related-card]");
      const drawerRow = origin.closest("[data-related-feedback-row]");
      const cards = Array.from(view.results[model.activeMode].querySelectorAll("[data-related-card]"));
      const index = cards.findIndex((item) => item.getAttribute("data-related-card") === candidate);
      const nextCandidate = index >= 0 ? cards[index + 1]?.getAttribute("data-related-card") || "" : "";
      const previousCandidate = index > 0 ? cards[index - 1]?.getAttribute("data-related-card") || "" : "";
      if (card) {
        setRelatedCardBusy(card, true);
      }
      if (drawerRow) {
        setRelatedFeedbackBusy(drawerRow, true);
      }
      setStatus(action === "open_editor" ? "Opening editor\u2026" : "Saving feedback\u2026", "is-loading");
      try {
        const result = await requestRelatedItem(fetcher, config.itemEndpoint, {
          source: config.source,
          candidate,
          action,
          token: config.csrfToken,
          ...until ? { until } : {}
        });
        if (action === "open_editor") {
          if (result.warning) {
            setStatus(`Editor opened. ${result.warning}`, "is-warning");
          } else {
            setStatus("Opened in editor and recorded in recall history.", "is-success");
          }
          return;
        }
        markInactiveModesStale();
        const activeMode = model.activeMode;
        const activeState = model.modes[activeMode];
        const refreshed = await refreshAfterMutation(activeMode);
        if (!refreshed) {
          setStatus(
            `Feedback was saved, but related notes could not be refreshed: ${activeState.error || "refresh failed"}`,
            "is-warning"
          );
          return;
        }
        const outcome = action === "dismissed" ? "Dismissed note." : action === "later" ? `Hidden until ${until}.` : action === "clear" ? "Cleared feedback." : "Marked note as useful.";
        setStatus(outcome, "is-success");
        focusAfterMutation(candidate, nextCandidate, previousCandidate, drawerRow !== null);
      } catch (error) {
        setStatus(errorMessage(error), "is-error");
      } finally {
        if (card?.isConnected) {
          setRelatedCardBusy(card, false);
        }
        if (drawerRow?.isConnected) {
          setRelatedFeedbackBusy(drawerRow, false);
        }
      }
    };
    for (const definition of relatedModeDefinitions) {
      const { mode } = definition;
      view.tabs[mode].addEventListener("click", () => selectMode(mode));
      view.tabs[mode].addEventListener("keydown", (event) => {
        const currentIndex = relatedModeDefinitions.findIndex((item) => item.mode === mode);
        let targetIndex = currentIndex;
        switch (event.key) {
          case "ArrowLeft":
            targetIndex = (currentIndex - 1 + relatedModeDefinitions.length) % relatedModeDefinitions.length;
            break;
          case "ArrowRight":
            targetIndex = (currentIndex + 1) % relatedModeDefinitions.length;
            break;
          case "Home":
            targetIndex = 0;
            break;
          case "End":
            targetIndex = relatedModeDefinitions.length - 1;
            break;
          default:
            return;
        }
        event.preventDefault();
        const targetMode = relatedModeDefinitions[targetIndex].mode;
        view.tabs[targetMode].focus();
        selectMode(targetMode);
      });
      view.moreButtons[mode].addEventListener("click", () => {
        const state = model.modes[mode];
        if (state.loading || state.limit >= RELATED_MAX_LIMIT || !state.response?.hasMore) {
          return;
        }
        void load(mode, nextRelatedLimit(state.limit));
      });
    }
    root.addEventListener("click", (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const link = target?.closest("[data-related-view-candidate]");
      if (link) {
        const candidate = link.getAttribute("data-related-view-candidate") || "";
        if (candidate && config.source && config.csrfToken) {
          trackRelatedView(fetcher, beacon, config.itemEndpoint, {
            source: config.source,
            candidate,
            token: config.csrfToken
          });
        }
        return;
      }
      const button = target?.closest("[data-related-action]");
      if (!button || button.disabled) {
        return;
      }
      let action = actionFromValue(button.getAttribute("data-related-action") || "");
      if (!action) {
        return;
      }
      if (action === "useful" && button.getAttribute("aria-pressed") === "true") {
        action = "clear";
      }
      void runMutation(
        action,
        button.getAttribute("data-related-candidate") || "",
        button.getAttribute("data-related-until") || "",
        button
      );
    });
    root.addEventListener("submit", (event) => {
      const form = event.target instanceof HTMLFormElement ? event.target : null;
      if (!form?.matches("[data-related-later-form]")) {
        return;
      }
      event.preventDefault();
      const input = form.querySelector("[data-related-later-date]");
      void runMutation("later", form.getAttribute("data-related-later-form") || "", input?.value || "", form);
    });
    if (config.source === "") {
      model.modes.balanced.error = "Related notes are unavailable for this page.";
      showStatus("balanced");
      return;
    }
    selectMode("balanced");
  };

  // internal/web/static_src/related.ts
  var initRelatedNotes = (deps = {}) => {
    const root = queryElement(document, "[data-related-notes]", HTMLElement);
    if (!root) {
      return;
    }
    createRelatedController(root, deps);
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

  // internal/web/static_src/synthesize-model.ts
  var SYNTHESIS_MAX_SUPPORTERS = 10;
  var SYNTHESIS_MAX_DRAFT_BYTES = 1 << 20;
  var attribute = (root, name) => (root.getAttribute(name) || "").trim();
  var parseMaxSupporters = (value) => {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) {
      return SYNTHESIS_MAX_SUPPORTERS;
    }
    const truncated = Math.trunc(parsed);
    return truncated > 0 ? Math.min(truncated, SYNTHESIS_MAX_SUPPORTERS) : SYNTHESIS_MAX_SUPPORTERS;
  };
  var readSynthesisConfig = (root) => {
    const anchorKey = attribute(root, "data-synthesize-anchor-key");
    const anchorPath = attribute(root, "data-synthesize-anchor-path");
    const purposes = attribute(root, "data-synthesize-purposes").split(",").map((purpose) => purpose.trim()).filter((purpose) => purpose !== "");
    const defaultPurpose = attribute(root, "data-synthesize-default-purpose");
    return {
      csrfToken: attribute(root, "data-synthesize-csrf"),
      vaultID: attribute(root, "data-synthesize-vault"),
      anchor: {
        key: anchorKey,
        title: attribute(root, "data-synthesize-anchor-title") || anchorPath || anchorKey,
        path: anchorPath,
        route: attribute(root, "data-synthesize-anchor-route")
      },
      defaultFolder: attribute(root, "data-synthesize-default-folder"),
      defaultPurpose: defaultPurpose || purposes[0] || "synthesis",
      purposes: purposes.length > 0 ? purposes : [defaultPurpose || "synthesis"],
      maxSupporters: parseMaxSupporters(attribute(root, "data-synthesize-max-supporters")),
      agentEnabled: attribute(root, "data-synthesize-agent") === "enabled",
      contextEndpoint: attribute(root, "data-synthesize-context-endpoint") || "/api/synthesis/context",
      generateEndpoint: attribute(root, "data-synthesize-generate-endpoint") || "/api/synthesis/generate",
      previewEndpoint: attribute(root, "data-synthesize-preview-endpoint") || "/api/synthesis/preview",
      applyEndpoint: attribute(root, "data-synthesize-apply-endpoint") || "/api/synthesis/apply",
      relatedEndpoint: attribute(root, "data-synthesize-related-endpoint") || "/api/related",
      searchEndpoint: attribute(root, "data-synthesize-search-endpoint") || "/api/search"
    };
  };
  var createSynthesisModel = (config) => ({
    sources: [],
    title: "",
    folder: config.defaultFolder,
    purpose: config.defaultPurpose,
    prompt: "",
    draft: "",
    candidate: null,
    context: null,
    contextStale: false,
    preview: null,
    previewStale: false
  });
  var invalidateSynthesisPreview = (model) => {
    if (model.preview === null) {
      return;
    }
    model.preview = null;
    model.previewStale = true;
  };
  var invalidateSynthesisContext = (model) => {
    invalidateSynthesisPreview(model);
    if (model.context === null) {
      return;
    }
    model.context = null;
    model.contextStale = true;
  };
  var synthesisSelectedKeys = (model) => model.sources.map((source) => source.key);
  var synthesisContextSignature = (model) => JSON.stringify([synthesisSelectedKeys(model), model.purpose, model.prompt]);
  var synthesisPreviewSignature = (model) => JSON.stringify([
    model.context === null ? "" : model.context.context_id,
    model.title.trim(),
    model.folder.trim(),
    model.draft
  ]);
  var synthesisSourcePosition = (model, key) => {
    const index = model.sources.findIndex((source) => source.key === key);
    return index < 0 ? 0 : index + 2;
  };
  var formatSynthesisBytes = (bytes) => {
    if (!Number.isFinite(bytes) || bytes < 0) {
      return "0 bytes";
    }
    const whole = Math.trunc(bytes);
    if (whole < 1024) {
      return `${whole} ${whole === 1 ? "byte" : "bytes"}`;
    }
    return `${whole} bytes (${(whole / 1024).toFixed(1)} KiB)`;
  };
  var synthesisDraftContainsSources = (draft) => {
    let fenced = false;
    for (const raw of draft.split("\n")) {
      const line = raw.replace(/\r$/, "");
      if (/^\s{0,3}(```+|~~~+)/.test(line)) {
        fenced = !fenced;
        continue;
      }
      if (fenced) {
        continue;
      }
      const match = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
      if (match && match[1].length === 2 && match[2].trim().normalize("NFC").toLowerCase() === "sources") {
        return true;
      }
    }
    return false;
  };
  var synthesisTitleError = (title) => {
    const trimmed = title.trim();
    if (trimmed === "") {
      return "A note title is required.";
    }
    if (trimmed.includes("/")) {
      return "A note title may not contain '/'.";
    }
    if (trimmed.startsWith(".")) {
      return "A note title may not start with '.'.";
    }
    if (/[[\]|#^]/.test(trimmed)) {
      return "A note title may not contain '[', ']', '|', '#', or '^'.";
    }
    return "";
  };
  var synthesisDraftError = (draft) => {
    if (new TextEncoder().encode(draft).length > SYNTHESIS_MAX_DRAFT_BYTES) {
      return `The draft is larger than the ${SYNTHESIS_MAX_DRAFT_BYTES} byte maximum.`;
    }
    if (draft.trim() === "") {
      return "A draft is required.";
    }
    if (synthesisDraftContainsSources(draft)) {
      return "The draft may not contain a '## Sources' heading; the note adds one from the selected sources.";
    }
    return "";
  };
  var synthesisFolderError = (folder) => {
    const trimmed = folder.trim();
    if (trimmed === "") {
      return "";
    }
    if (trimmed.startsWith("/")) {
      return "A folder must be vault-relative.";
    }
    if (trimmed.split("/").some((segment) => segment === ".." || segment === ".")) {
      return "A folder may not contain '.' or '..' segments.";
    }
    return "";
  };
  var synthesisDestinationPath = (model) => {
    const title = model.title.trim();
    if (title === "") {
      return "";
    }
    const folder = model.folder.trim().replace(/^\/+|\/+$/g, "");
    return folder === "" ? `${title}.md` : `${folder}/${title}.md`;
  };
  var formatSynthesisExpiry = (value) => {
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) {
      return value;
    }
    return parsed.toLocaleString();
  };
  var synthesisPurposeLabel = (purpose) => {
    switch (purpose) {
      case "synthesis":
        return "Synthesis";
      case "contradictions":
        return "Contradictions";
      case "questions":
        return "Questions";
      default:
        return purpose;
    }
  };

  // internal/web/static_src/synthesize-render.ts
  var suggestionID = (kind, mode) => `synthesize-${kind}-${mode}`;
  var element = (doc, tag, className, attributes = {}) => {
    const node = doc.createElement(tag);
    if (className !== "") {
      node.className = className;
    }
    for (const [name, value] of Object.entries(attributes)) {
      node.setAttribute(name, value);
    }
    return node;
  };
  var labelled = (doc, id, text, control) => {
    const wrapper = element(doc, "div", "synthesize-field");
    const label = element(doc, "label", "synthesize-field-label");
    label.setAttribute("for", id);
    label.textContent = text;
    control.id = id;
    wrapper.append(label, control);
    return wrapper;
  };
  var labelledWithError = (doc, id, text, control, errorAttribute) => {
    const wrapper = labelled(doc, id, text, control);
    const error = element(doc, "p", "synthesize-field-error", { [errorAttribute]: "" });
    error.id = `${id}-error`;
    error.hidden = true;
    control.setAttribute("aria-describedby", error.id);
    wrapper.appendChild(error);
    return { wrapper, error };
  };
  var buildSourcesSection = (doc) => {
    const section = element(doc, "section", "synthesize-section synthesize-sources", {
      "aria-labelledby": "synthesize-sources-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-sources-heading";
    heading.textContent = "Sources";
    const intro = element(doc, "p", "synthesize-section-intro");
    intro.textContent = "The anchor is always first. Drag a handle or press Up and Down on it to reorder supporting notes.";
    const list = element(doc, "ol", "synthesize-source-list", {
      "data-synthesize-source-list": ""
    });
    const status = element(doc, "p", "synthesize-selection-status", {
      "data-synthesize-selection-status": ""
    });
    status.tabIndex = -1;
    section.append(heading, intro, list, status);
    return { section, list, status };
  };
  var buildSuggestionsSection = (doc) => {
    const section = element(doc, "section", "synthesize-section synthesize-suggestions", {
      "aria-labelledby": "synthesize-suggestions-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-suggestions-heading";
    heading.textContent = "Suggested notes";
    const tablist = element(doc, "div", "synthesize-tab-list", {
      role: "tablist",
      "aria-label": "Suggestion modes"
    });
    const status = element(doc, "p", "synthesize-suggestions-status", {
      "data-synthesize-suggestions-status": "",
      role: "status",
      "aria-live": "polite"
    });
    const panelsContainer = element(doc, "div", "synthesize-suggestion-panels");
    const tabs = {};
    const panels = {};
    const results = {};
    for (const definition of relatedModeDefinitions) {
      const active = definition.mode === "balanced";
      const tab = element(doc, "button", "synthesize-tab", {
        role: "tab",
        "aria-controls": suggestionID("panel", definition.mode),
        "aria-selected": active ? "true" : "false",
        "data-synthesize-tab": definition.mode
      });
      tab.type = "button";
      tab.id = suggestionID("tab", definition.mode);
      tab.tabIndex = active ? 0 : -1;
      tab.textContent = definition.label;
      tablist.appendChild(tab);
      const panel = element(doc, "div", "synthesize-suggestion-panel", {
        role: "tabpanel",
        "aria-labelledby": suggestionID("tab", definition.mode),
        "data-synthesize-panel": definition.mode
      });
      panel.id = suggestionID("panel", definition.mode);
      panel.hidden = !active;
      const description = element(doc, "p", "synthesize-tab-description");
      description.textContent = definition.description;
      const list = element(doc, "div", "synthesize-candidates", {
        "data-synthesize-suggestions": definition.mode
      });
      panel.append(description, list);
      panelsContainer.appendChild(panel);
      tabs[definition.mode] = tab;
      panels[definition.mode] = panel;
      results[definition.mode] = list;
    }
    section.append(heading, tablist, status, panelsContainer);
    return { section, tabs, panels, results, status };
  };
  var buildSearchSection = (doc) => {
    const section = element(doc, "section", "synthesize-section synthesize-search", {
      "aria-labelledby": "synthesize-search-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-search-heading";
    heading.textContent = "Search the vault";
    const form = element(doc, "form", "synthesize-search-form", {
      "data-synthesize-search-form": "",
      role: "search"
    });
    const input = element(doc, "input", "synthesize-search-input", {
      "data-synthesize-search-input": "",
      type: "search",
      placeholder: "Find a note to add"
    });
    input.name = "q";
    const submit = element(doc, "button", "synthesize-search-submit");
    submit.type = "submit";
    submit.textContent = "Search";
    form.append(labelled(doc, "synthesize-search-query", "Search notes", input), submit);
    const status = element(doc, "p", "synthesize-search-status", {
      "data-synthesize-search-status": "",
      role: "status",
      "aria-live": "polite"
    });
    const results = element(doc, "div", "synthesize-candidates", {
      "data-synthesize-search-results": ""
    });
    section.append(heading, form, status, results);
    return { section, form, input, results, status };
  };
  var buildDraftSection = (doc, config) => {
    const section = element(doc, "section", "synthesize-section synthesize-draft", {
      "aria-labelledby": "synthesize-draft-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-draft-heading";
    heading.textContent = "New note";
    const title = element(doc, "input", "synthesize-input", {
      "data-synthesize-title": "",
      type: "text"
    });
    title.name = "title";
    const folder = element(doc, "input", "synthesize-input", {
      "data-synthesize-folder": "",
      type: "text",
      placeholder: "Vault root"
    });
    folder.name = "folder";
    const purpose = element(doc, "select", "synthesize-input", {
      "data-synthesize-purpose": ""
    });
    purpose.name = "purpose";
    for (const value of config.purposes) {
      const option = doc.createElement("option");
      option.value = value;
      option.textContent = synthesisPurposeLabel(value);
      purpose.appendChild(option);
    }
    const prompt = element(doc, "textarea", "synthesize-textarea", {
      "data-synthesize-prompt": "",
      rows: "3",
      placeholder: "Optional question or angle for this research packet"
    });
    prompt.name = "prompt";
    const draft = element(doc, "textarea", "synthesize-textarea synthesize-draft-body", {
      "data-synthesize-draft": "",
      rows: "12",
      placeholder: "Write the note that says what these sources add up to"
    });
    draft.name = "draft";
    const titleField = labelledWithError(
      doc,
      "synthesize-title",
      "Title",
      title,
      "data-synthesize-title-error"
    );
    const folderField = labelledWithError(
      doc,
      "synthesize-folder",
      "Folder",
      folder,
      "data-synthesize-folder-error"
    );
    const draftField = labelledWithError(
      doc,
      "synthesize-draft",
      "Draft",
      draft,
      "data-synthesize-draft-error"
    );
    const grid = element(doc, "div", "synthesize-field-grid");
    grid.append(titleField.wrapper, folderField.wrapper, labelled(doc, "synthesize-purpose", "Purpose", purpose));
    section.append(
      heading,
      grid,
      labelled(doc, "synthesize-prompt", "Prompt", prompt),
      draftField.wrapper
    );
    return {
      section,
      title,
      titleError: titleField.error,
      folder,
      folderError: folderField.error,
      purpose,
      prompt,
      draft,
      draftError: draftField.error
    };
  };
  var buildGenerateSection = (doc, config) => {
    const section = element(doc, "section", "synthesize-section synthesize-generate", {
      "aria-labelledby": "synthesize-generate-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-generate-heading";
    heading.textContent = "Generated candidate";
    const intro = element(doc, "p", "synthesize-section-intro");
    intro.textContent = config.agentEnabled ? "Generation sends the prepared context packet to your configured [agent].command. The candidate is shown here and never replaces your draft on its own." : "Generation is unavailable until [agent].command is configured. Everything else on this page works without an agent.";
    const actions = element(doc, "div", "synthesize-generate-actions");
    const generate = element(doc, "button", "synthesize-action", {
      "data-synthesize-generate": ""
    });
    generate.type = "button";
    generate.textContent = "Generate draft";
    generate.disabled = true;
    actions.appendChild(generate);
    const status = element(doc, "p", "synthesize-generate-status", {
      "data-synthesize-generate-status": "",
      role: "status",
      "aria-live": "polite"
    });
    const panel = element(doc, "article", "synthesize-candidate-panel", {
      "data-synthesize-candidate-panel": "",
      "aria-labelledby": "synthesize-generate-heading"
    });
    panel.hidden = true;
    panel.tabIndex = -1;
    const rendered = element(doc, "div", "synthesize-candidate-rendered", {
      "data-synthesize-candidate-rendered": ""
    });
    const markdownWrapper = element(doc, "details", "synthesize-candidate-markdown");
    const markdownSummary = doc.createElement("summary");
    markdownSummary.textContent = "Candidate Markdown";
    const markdown = element(doc, "pre", "synthesize-packet-body", {
      "data-synthesize-candidate-markdown": "",
      tabindex: "0"
    });
    markdownWrapper.append(markdownSummary, markdown);
    const candidateActions = element(doc, "div", "synthesize-candidate-actions");
    const replace = element(doc, "button", "synthesize-action", {
      "data-synthesize-candidate-replace": ""
    });
    replace.type = "button";
    replace.textContent = "Replace draft";
    const append = element(doc, "button", "synthesize-action", {
      "data-synthesize-candidate-append": ""
    });
    append.type = "button";
    append.textContent = "Append to draft";
    const copy = element(doc, "button", "synthesize-action", {
      "data-synthesize-candidate-copy": ""
    });
    copy.type = "button";
    copy.textContent = "Copy candidate";
    const dismiss = element(doc, "button", "synthesize-action is-quiet", {
      "data-synthesize-candidate-dismiss": ""
    });
    dismiss.type = "button";
    dismiss.textContent = "Dismiss candidate";
    candidateActions.append(replace, append, copy, dismiss);
    panel.append(rendered, markdownWrapper, candidateActions);
    section.append(heading, intro, actions, status, panel);
    return { section, generate, status, panel, rendered, markdown, replace, append, copy, dismiss };
  };
  var buildCreateSection = (doc) => {
    const section = element(doc, "section", "synthesize-section synthesize-create", {
      "aria-labelledby": "synthesize-create-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-create-heading";
    heading.textContent = "Create note";
    const destination = element(doc, "p", "synthesize-destination", {
      "data-synthesize-destination": ""
    });
    const actions = element(doc, "div", "synthesize-create-actions");
    const preview = element(doc, "button", "synthesize-action is-primary", {
      "data-synthesize-preview": ""
    });
    preview.type = "button";
    preview.textContent = "Preview note";
    const apply = element(doc, "button", "synthesize-action", { "data-synthesize-apply": "" });
    apply.type = "button";
    apply.textContent = "Create note";
    apply.disabled = true;
    actions.append(preview, apply);
    const status = element(doc, "p", "synthesize-preview-status", {
      "data-synthesize-preview-status": "",
      role: "status",
      "aria-live": "polite"
    });
    const panel = element(doc, "div", "synthesize-preview-panel", {
      "data-synthesize-preview-panel": ""
    });
    panel.hidden = true;
    panel.tabIndex = -1;
    const previewDestination = element(doc, "p", "synthesize-preview-destination", {
      "data-synthesize-preview-destination": ""
    });
    const expiry = element(doc, "p", "synthesize-preview-expiry", {
      "data-synthesize-preview-expiry": ""
    });
    const rendered = element(doc, "div", "synthesize-preview-rendered", {
      "data-synthesize-preview-rendered": ""
    });
    const markdownWrapper = element(doc, "details", "synthesize-preview-markdown");
    const markdownSummary = doc.createElement("summary");
    markdownSummary.textContent = "Note Markdown";
    const markdown = element(doc, "pre", "synthesize-packet-body", {
      "data-synthesize-preview-markdown": "",
      tabindex: "0"
    });
    markdownWrapper.append(markdownSummary, markdown);
    const sourcesHeading = element(doc, "h4", "synthesize-manifest-heading");
    sourcesHeading.textContent = "Sources";
    const sources = element(doc, "ol", "synthesize-preview-sources", {
      "data-synthesize-preview-sources": ""
    });
    panel.append(previewDestination, expiry, rendered, markdownWrapper, sourcesHeading, sources);
    section.append(heading, destination, actions, status, panel);
    return {
      section,
      destination,
      preview,
      apply,
      status,
      panel,
      previewDestination,
      expiry,
      rendered,
      markdown,
      sources
    };
  };
  var buildContextSection = (doc) => {
    const section = element(doc, "section", "synthesize-section synthesize-context", {
      "aria-labelledby": "synthesize-context-heading"
    });
    const heading = element(doc, "h3", "synthesize-section-heading");
    heading.id = "synthesize-context-heading";
    heading.textContent = "Context packet";
    const actions = element(doc, "div", "synthesize-context-actions");
    const prepare = element(doc, "button", "synthesize-action is-primary", {
      "data-synthesize-prepare": ""
    });
    prepare.type = "button";
    prepare.textContent = "Prepare context";
    const copy = element(doc, "button", "synthesize-action", { "data-synthesize-copy": "" });
    copy.type = "button";
    copy.textContent = "Copy context";
    copy.disabled = true;
    const discard = element(doc, "button", "synthesize-action is-quiet", {
      "data-synthesize-discard": ""
    });
    discard.type = "button";
    discard.textContent = "Discard saved work";
    actions.append(prepare, copy, discard);
    const status = element(doc, "p", "synthesize-context-status", {
      "data-synthesize-context-status": "",
      role: "status",
      "aria-live": "polite"
    });
    const summary = element(doc, "p", "synthesize-context-summary", {
      "data-synthesize-context-summary": ""
    });
    const manifest = element(doc, "div", "synthesize-manifest", {
      "data-synthesize-manifest": ""
    });
    const packetWrapper = element(doc, "details", "synthesize-packet");
    const packetSummary = doc.createElement("summary");
    packetSummary.textContent = "Packet Markdown";
    const packet = element(doc, "pre", "synthesize-packet-body", {
      "data-synthesize-packet": "",
      tabindex: "0"
    });
    packetWrapper.append(packetSummary, packet);
    section.append(heading, actions, status, summary, manifest, packetWrapper);
    return { section, prepare, copy, discard, status, summary, manifest, packet };
  };
  var createSynthesisView = (mount, config) => {
    const doc = mount.ownerDocument;
    const warning = element(doc, "p", "synthesize-warning", {
      "data-synthesize-warning": "",
      role: "status",
      "aria-live": "polite"
    });
    warning.hidden = true;
    const sources = buildSourcesSection(doc);
    const suggestions = buildSuggestionsSection(doc);
    const search = buildSearchSection(doc);
    const draft = buildDraftSection(doc, config);
    const context = buildContextSection(doc);
    const generate = buildGenerateSection(doc, config);
    const create = buildCreateSection(doc);
    const columns = element(doc, "div", "synthesize-columns");
    const left = element(doc, "div", "synthesize-column");
    left.append(sources.section, suggestions.section, search.section);
    const right = element(doc, "div", "synthesize-column");
    right.append(draft.section, context.section, generate.section, create.section);
    columns.append(left, right);
    mount.replaceChildren(warning, columns);
    return {
      root: mount,
      warning,
      sourceList: sources.list,
      selectionStatus: sources.status,
      suggestionTabs: suggestions.tabs,
      suggestionPanels: suggestions.panels,
      suggestionResults: suggestions.results,
      suggestionStatus: suggestions.status,
      searchForm: search.form,
      searchInput: search.input,
      searchResults: search.results,
      searchStatus: search.status,
      titleInput: draft.title,
      titleError: draft.titleError,
      folderInput: draft.folder,
      folderError: draft.folderError,
      purposeSelect: draft.purpose,
      promptInput: draft.prompt,
      draftInput: draft.draft,
      draftError: draft.draftError,
      prepareButton: context.prepare,
      copyButton: context.copy,
      discardButton: context.discard,
      contextStatus: context.status,
      contextSummary: context.summary,
      manifest: context.manifest,
      packet: context.packet,
      generateButton: generate.generate,
      generateStatus: generate.status,
      candidatePanel: generate.panel,
      candidateRendered: generate.rendered,
      candidateMarkdown: generate.markdown,
      candidateReplaceButton: generate.replace,
      candidateAppendButton: generate.append,
      candidateCopyButton: generate.copy,
      candidateDismissButton: generate.dismiss,
      destination: create.destination,
      previewButton: create.preview,
      applyButton: create.apply,
      previewStatus: create.status,
      previewPanel: create.panel,
      previewDestination: create.previewDestination,
      previewExpiry: create.expiry,
      previewRendered: create.rendered,
      previewMarkdown: create.markdown,
      previewSources: create.sources
    };
  };
  var selectSynthesisSuggestionMode = (view, activeMode) => {
    for (const definition of relatedModeDefinitions) {
      const selected = definition.mode === activeMode;
      view.suggestionTabs[definition.mode].setAttribute(
        "aria-selected",
        selected ? "true" : "false"
      );
      view.suggestionTabs[definition.mode].tabIndex = selected ? 0 : -1;
      view.suggestionPanels[definition.mode].hidden = !selected;
    }
  };
  var sourceRow = (doc, position, total, source, anchor) => {
    const row = element(doc, "li", "synthesize-source", {
      "data-synthesize-source": source.key,
      "data-synthesize-position": String(position)
    });
    if (anchor) {
      row.classList.add("is-anchor");
      row.setAttribute("data-synthesize-anchor", "");
    }
    const badge = element(doc, "span", "synthesize-source-position");
    badge.textContent = String(position);
    const body = element(doc, "div", "synthesize-source-body");
    const link = element(doc, "a", "synthesize-source-title");
    link.href = source.route;
    link.textContent = source.title;
    const path = element(doc, "code", "synthesize-source-path");
    path.textContent = source.path;
    body.append(link, path);
    row.append(badge, body);
    if (anchor) {
      const note = element(doc, "span", "synthesize-source-role");
      note.textContent = "Anchor";
      row.appendChild(note);
      return row;
    }
    const controls = element(doc, "div", "synthesize-source-controls");
    const handle = element(doc, "button", "synthesize-source-handle", {
      "data-synthesize-handle": source.key,
      "aria-label": `Reorder ${source.title}, position ${position} of ${total}. Press Up or Down to move it.`
    });
    handle.type = "button";
    handle.textContent = "Drag";
    const remove = element(doc, "button", "synthesize-source-remove", {
      "data-synthesize-remove": source.key,
      "aria-label": `Remove ${source.title}`
    });
    remove.type = "button";
    remove.textContent = "Remove";
    controls.append(handle, remove);
    row.appendChild(controls);
    return row;
  };
  var renderSynthesisSources = (view, model, config) => {
    const doc = view.sourceList.ownerDocument;
    const total = model.sources.length + 1;
    const rows = [sourceRow(doc, 1, total, config.anchor, true)];
    model.sources.forEach((source, index) => {
      rows.push(sourceRow(doc, index + 2, total, source, false));
    });
    view.sourceList.replaceChildren(...rows);
    const remaining = config.maxSupporters - model.sources.length;
    view.selectionStatus.textContent = remaining > 0 ? `${total} of ${config.maxSupporters + 1} notes selected. ${remaining} more can be added.` : `${total} of ${config.maxSupporters + 1} notes selected. Remove one to add another.`;
  };
  var renderSynthesisCandidates = (container, candidates, selectedKeys, anchorKey, detailIsHTML, emptyMessage) => {
    const doc = container.ownerDocument;
    const selected = new Set(selectedKeys);
    const rows = candidates.filter((candidate) => candidate.key !== anchorKey && candidate.key !== "").map((candidate) => {
      const card = element(doc, "article", "synthesize-candidate", {
        "data-synthesize-candidate": candidate.key
      });
      const heading = element(doc, "h4", "synthesize-candidate-title");
      const link = element(doc, "a", "");
      link.href = candidate.route;
      link.textContent = candidate.title;
      heading.appendChild(link);
      const path = element(doc, "p", "synthesize-candidate-path");
      const pathCode = doc.createElement("code");
      pathCode.textContent = candidate.path;
      path.appendChild(pathCode);
      card.append(heading, path);
      if (candidate.detail !== "") {
        const detail = element(doc, "p", "synthesize-candidate-detail");
        if (detailIsHTML) {
          detail.innerHTML = candidate.detail;
        } else {
          detail.textContent = candidate.detail;
        }
        card.appendChild(detail);
      }
      const add = element(doc, "button", "synthesize-candidate-add", {
        "data-synthesize-add": candidate.key
      });
      add.type = "button";
      if (selected.has(candidate.key)) {
        add.disabled = true;
        add.textContent = "Selected";
      } else {
        add.textContent = "Add source";
        add.setAttribute("aria-label", `Add ${candidate.title} as a source`);
      }
      card.appendChild(add);
      return card;
    });
    if (rows.length === 0) {
      const empty = element(doc, "p", "synthesize-candidates-empty");
      empty.textContent = emptyMessage;
      container.replaceChildren(empty);
      return;
    }
    container.replaceChildren(...rows);
  };
  var setSynthesisWarning = (view, message, link) => {
    view.warning.textContent = message;
    if (message !== "" && link) {
      const anchor = element(view.warning.ownerDocument, "a", "synthesize-warning-link", {
        "data-synthesize-warning-link": "",
        href: link.href
      });
      anchor.textContent = link.label;
      view.warning.append(" ", anchor);
    }
    view.warning.hidden = message === "";
  };
  var excerptLabel = (path, start, end) => start === end ? `${path}:${start}` : `${path}:${start}-${end}`;
  var renderSynthesisContext = (view, model) => {
    const doc = view.manifest.ownerDocument;
    view.prepareButton.textContent = model.context === null && !model.contextStale ? "Prepare context" : "Rebuild context";
    if (model.context === null) {
      view.copyButton.disabled = true;
      view.contextSummary.textContent = "";
      view.manifest.replaceChildren();
      view.packet.textContent = "";
      return;
    }
    const context = model.context;
    view.copyButton.disabled = false;
    view.contextSummary.textContent = `${context.manifest.notes.length} notes, ${formatSynthesisBytes(context.manifest.byte_count)} of a ${formatSynthesisBytes(context.manifest.max_bytes)} budget, purpose ${synthesisPurposeLabel(context.purpose)}.`;
    const notesHeading = element(doc, "h4", "synthesize-manifest-heading");
    notesHeading.textContent = "Selected notes";
    const notes = element(doc, "ol", "synthesize-manifest-notes");
    for (const note of context.manifest.notes) {
      const item = doc.createElement("li");
      const link = doc.createElement("a");
      link.href = note.route;
      link.textContent = note.title || note.path;
      const path = doc.createElement("code");
      path.textContent = note.path;
      item.append(link, " ", path);
      notes.appendChild(item);
    }
    const excerptsHeading = element(doc, "h4", "synthesize-manifest-heading");
    excerptsHeading.textContent = "Excerpt locations";
    const excerpts = element(doc, "ul", "synthesize-manifest-excerpts");
    if (context.manifest.excerpts.length === 0) {
      const item = doc.createElement("li");
      item.textContent = "None.";
      excerpts.appendChild(item);
    } else {
      for (const excerpt of context.manifest.excerpts) {
        const item = doc.createElement("li");
        const code = doc.createElement("code");
        code.textContent = excerptLabel(excerpt.path, excerpt.start_line, excerpt.end_line);
        item.appendChild(code);
        excerpts.appendChild(item);
      }
    }
    view.manifest.replaceChildren(notesHeading, notes, excerptsHeading, excerpts);
    view.packet.textContent = context.markdown;
  };
  var setSynthesisFieldError = (target, message) => {
    target.textContent = message;
    target.hidden = message === "";
  };
  var renderSynthesisCandidate = (view, model) => {
    const candidate = model.candidate;
    if (candidate === null) {
      view.candidatePanel.hidden = true;
      view.candidateRendered.replaceChildren();
      view.candidateMarkdown.textContent = "";
      return;
    }
    view.candidatePanel.hidden = false;
    view.candidateMarkdown.textContent = candidate.markdown;
    if (candidate.html !== "") {
      view.candidateRendered.innerHTML = candidate.html;
      return;
    }
    const doc = view.candidateRendered.ownerDocument;
    const note = element(doc, "p", "synthesize-candidate-plain");
    note.textContent = "Restored candidate \u2014 shown as Markdown.";
    view.candidateRendered.replaceChildren(note);
  };
  var renderSynthesisDestination = (view, model) => {
    const destination = synthesisDestinationPath(model);
    view.destination.textContent = destination === "" ? "Enter a title to choose where the note is created." : `Creates ${destination}`;
  };
  var renderSynthesisPreview = (view, model) => {
    const preview = model.preview;
    view.applyButton.disabled = preview === null;
    if (preview === null) {
      view.previewPanel.hidden = true;
      view.previewDestination.textContent = "";
      view.previewExpiry.textContent = "";
      view.previewRendered.replaceChildren();
      view.previewMarkdown.textContent = "";
      view.previewSources.replaceChildren();
      return;
    }
    const doc = view.previewSources.ownerDocument;
    view.previewPanel.hidden = false;
    view.previewDestination.textContent = `Creates ${preview.destination}`;
    view.previewExpiry.textContent = `This preview can be applied until ${formatSynthesisExpiry(preview.expires_at)}.`;
    view.previewRendered.innerHTML = preview.rendered_html;
    view.previewMarkdown.textContent = preview.markdown;
    const notes = model.context ? model.context.manifest.notes : [];
    const rows = notes.map((note) => {
      const item = doc.createElement("li");
      const link = doc.createElement("a");
      link.href = note.route;
      link.textContent = note.title || note.path;
      const path = doc.createElement("code");
      path.textContent = note.path;
      item.append(link, " ", path);
      return item;
    });
    if (rows.length === 0) {
      const item = doc.createElement("li");
      item.textContent = "Listed in the note Markdown above.";
      view.previewSources.replaceChildren(item);
      return;
    }
    view.previewSources.replaceChildren(...rows);
  };

  // internal/web/static_src/synthesize-requests.ts
  var SYNTHESIS_SUGGESTION_LIMIT = 8;
  var requestSynthesisSuggestions = (fetcher, endpoint, source, mode) => requestRelated(fetcher, endpoint, source, mode, SYNTHESIS_SUGGESTION_LIMIT);
  var resolveSynthesisSource = async (fetcher, endpoint, key) => {
    try {
      const response = await requestRelated(fetcher, endpoint, key, "balanced", 1);
      return {
        key: response.source.key,
        title: response.source.title || response.source.path,
        path: response.source.path,
        route: response.source.route
      };
    } catch {
      return null;
    }
  };
  var buildSynthesisSearchURL = (endpoint, query) => {
    const params = new URLSearchParams();
    params.set("q", query);
    return `${endpoint}?${params.toString()}`;
  };
  var requestSynthesisSearch = async (fetcher, endpoint, query) => {
    const response = await fetcher(buildSynthesisSearchURL(endpoint, query), {
      headers: { Accept: "application/json" }
    });
    const payload = await readResponsePayload(response);
    if (!response.ok) {
      throw new Error(normalizeManagementErrorMessage(response.status, payload));
    }
    return normalizeSearchResults(payload);
  };

  // internal/web/static_src/synthesize-sources.ts
  var candidateFromRelatedResult = (result) => ({
    key: result.candidate.key,
    title: result.candidate.title || result.candidate.path,
    path: result.candidate.path,
    route: result.candidate.route,
    detail: result.excerpt
  });
  var candidateFromSearchResult = (result) => {
    const key = (result.key || "").trim();
    if (key === "") {
      return null;
    }
    return {
      key,
      title: result.title || key,
      path: key,
      route: searchResultPath(result),
      detail: result.snippet
    };
  };
  var addSynthesisSource = (model, config, candidate) => {
    if (candidate.key === "") {
      return { ok: false, message: "That note cannot be added as a source." };
    }
    if (candidate.key === config.anchor.key) {
      return { ok: false, message: "The anchor note is already the first source." };
    }
    if (model.sources.some((source) => source.key === candidate.key)) {
      return { ok: false, message: `${candidate.title} is already selected.` };
    }
    if (model.sources.length >= config.maxSupporters) {
      return {
        ok: false,
        message: `Only ${config.maxSupporters} supporting notes can be selected. Remove one first.`
      };
    }
    model.sources.push({
      key: candidate.key,
      title: candidate.title,
      path: candidate.path,
      route: candidate.route
    });
    invalidateSynthesisContext(model);
    return {
      ok: true,
      message: `Added ${candidate.title} as source ${model.sources.length + 1} of ${model.sources.length + 1}.`
    };
  };
  var removeSynthesisSource = (model, key) => {
    const index = model.sources.findIndex((source) => source.key === key);
    if (index < 0) {
      return { ok: false, message: "That note is not selected." };
    }
    const [removed] = model.sources.splice(index, 1);
    invalidateSynthesisContext(model);
    return { ok: true, message: `Removed ${removed.title}.` };
  };
  var moveSynthesisSource = (model, key, direction) => {
    const index = model.sources.findIndex((source) => source.key === key);
    if (index < 0) {
      return { ok: false, message: "That note is not selected." };
    }
    const target = index + direction;
    if (target < 0 || target >= model.sources.length) {
      return { ok: false, message: `${model.sources[index].title} is already at the edge of the order.` };
    }
    const moved = model.sources[index];
    model.sources[index] = model.sources[target];
    model.sources[target] = moved;
    invalidateSynthesisContext(model);
    return {
      ok: true,
      message: `Moved ${moved.title} to position ${target + 2} of ${model.sources.length + 1}.`
    };
  };
  var reorderSynthesisSources = (model, keys) => {
    const byKey = new Map(model.sources.map((source) => [source.key, source]));
    const ordered = [];
    for (const key of keys) {
      const source = byKey.get(key);
      if (source && !ordered.includes(source)) {
        ordered.push(source);
      }
    }
    for (const source of model.sources) {
      if (!ordered.includes(source)) {
        ordered.push(source);
      }
    }
    const unchanged = ordered.every((source, index) => source === model.sources[index]);
    if (unchanged) {
      return { ok: false, message: "" };
    }
    model.sources = ordered;
    invalidateSynthesisContext(model);
    return { ok: true, message: `Reordered sources: ${ordered.length + 1} in total.` };
  };
  var dedupeSynthesisSources = (sources, anchorKey, maxSupporters) => {
    const seen = /* @__PURE__ */ new Set();
    const kept = [];
    for (const source of sources) {
      if (source.key === "" || source.key === anchorKey || seen.has(source.key)) {
        continue;
      }
      seen.add(source.key);
      kept.push(source);
      if (kept.length >= maxSupporters) {
        break;
      }
    }
    return kept;
  };

  // internal/web/static_src/synthesize-storage.ts
  var SYNTHESIS_STORAGE_VERSION = 2;
  var synthesisStorageKey = (vaultID, anchorKey) => `bn.synthesize.${vaultID}.${anchorKey}`;
  var safeSynthesisStorage = (view) => {
    if (!view) {
      return null;
    }
    try {
      return view.localStorage;
    } catch {
      return null;
    }
  };
  var readText2 = (value) => typeof value === "string" ? value : "";
  var readKeys = (value) => Array.isArray(value) ? value.flatMap((item) => {
    const key = typeof item === "string" ? item.trim() : "";
    return key === "" ? [] : [key];
  }) : [];
  var synthesisStateFromModel = (model) => ({
    sources: model.sources.map((source) => source.key),
    title: model.title,
    folder: model.folder,
    purpose: model.purpose,
    prompt: model.prompt,
    draft: model.draft,
    candidate: model.candidate ? model.candidate.markdown : ""
  });
  var readSynthesisState = (storage, key) => {
    if (!storage) {
      return null;
    }
    let raw;
    try {
      raw = storage.getItem(key);
    } catch {
      return null;
    }
    if (!raw) {
      return null;
    }
    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch {
      return null;
    }
    const record = asRecord(parsed);
    if (!record || readCount(record.version) !== SYNTHESIS_STORAGE_VERSION) {
      return null;
    }
    return {
      sources: readKeys(record.sources),
      title: readText2(record.title),
      folder: readText2(record.folder),
      purpose: readText2(record.purpose),
      prompt: readText2(record.prompt),
      draft: readText2(record.draft),
      candidate: readText2(record.candidate)
    };
  };
  var writeSynthesisState = (storage, key, state) => {
    if (!storage) {
      return false;
    }
    try {
      storage.setItem(key, JSON.stringify({ version: SYNTHESIS_STORAGE_VERSION, ...state }));
      return true;
    } catch {
      return false;
    }
  };
  var clearSynthesisState = (storage, key) => {
    if (!storage) {
      return false;
    }
    try {
      storage.removeItem(key);
      return true;
    } catch {
      return false;
    }
  };

  // internal/web/static_src/synthesize.ts
  var errorMessage2 = (error, fallback) => error instanceof Error && error.message !== "" ? error.message : fallback;
  var errorCode = (error) => error instanceof SynthesisRequestError ? error.code : "";
  var modeLabel = (mode) => relatedModeDefinitions.find((definition) => definition.mode === mode)?.label || mode;
  var SUGGESTIONS_EMPTY = "No suggestions are available in this mode.";
  var SEARCH_EMPTY = "No notes matched that search.";
  var CONTEXT_GONE = "The context packet has expired or is no longer valid. Rebuild it, then preview again. Your draft is unchanged.";
  var CONTEXT_SUPERSEDED = "The sources, purpose, or prompt changed while that packet was building, so it was discarded. Prepare the context again.";
  var PREVIEW_SUPERSEDED = "The note changed while that preview was building, so it was discarded. Preview again.";
  var DISCLOSURE = [
    "Generate sends the prepared context packet to your configured [agent].command.",
    "The packet contains the anchor note's complete body, exact excerpts from every selected source, and your prompt.",
    "Your prompt and that packet are retained in server memory until the context expires, is evicted to keep memory bounded, or the server restarts, and are never written to disk.",
    "The stored packet is sent to the command only after you accept this notice. The generated candidate is not stored on the server and never enters your draft on its own. Continue?"
  ].join("\n");
  var DISCARD_CONFIRMATION = "Discard the saved sources, draft, and generated candidate for this note? This cannot be undone.";
  var SAVED_STATE_STUCK = "This browser refused to delete the saved sources and draft for this note, so they will come back the next time you open the workbench.";
  var createSynthesisController = (root, mount, deps = {}) => {
    const config = readSynthesisConfig(root);
    const model = createSynthesisModel(config);
    const view = createSynthesisView(mount, config);
    const fetcher = deps.fetch || ((input, init) => fetch(input, init));
    const storage = deps.storage === void 0 ? safeSynthesisStorage(root.ownerDocument.defaultView) : deps.storage;
    const clipboard = deps.clipboard === void 0 ? (typeof navigator !== "undefined" ? navigator.clipboard : void 0) || null : deps.clipboard;
    const confirmer = deps.confirm || ((message) => window.confirm(message));
    const redirect = deps.redirect || ((path) => window.location.assign(path));
    const storageKey = synthesisStorageKey(config.vaultID, config.anchor.key);
    const statusEl = queryElement(root, "[data-synthesize-status]", HTMLElement);
    const fallback = queryElement(root, "[data-synthesize-fallback]", HTMLElement);
    if (fallback) {
      fallback.hidden = true;
    }
    const candidates = /* @__PURE__ */ new Map();
    const suggestionRequests = {
      balanced: 0,
      rediscover: 0,
      connect: 0
    };
    const suggestionEntries = {
      balanced: null,
      rediscover: null,
      connect: null
    };
    let searchEntries = null;
    let activeMode = "balanced";
    let searchRequest = 0;
    let storageWarned = false;
    let disclosureAccepted = false;
    let generating = false;
    let previewing = false;
    let applying = false;
    let showFieldErrors = false;
    const buttonByAttribute = (attribute2, value) => {
      for (const button of view.sourceList.querySelectorAll(`[${attribute2}]`)) {
        if (button.getAttribute(attribute2) === value) {
          return button;
        }
      }
      return null;
    };
    const announce = (message, kind = "") => {
      setStatusText(statusEl, message, kind);
    };
    const persist = () => {
      if (writeSynthesisState(storage, storageKey, synthesisStateFromModel(model))) {
        return;
      }
      if (storage === null || storageWarned) {
        return;
      }
      storageWarned = true;
      setSynthesisWarning(
        view,
        "This browser refused to save your work, so leaving the page will lose the current selection and draft."
      );
    };
    const clearSaved = () => clearSynthesisState(storage, storageKey) || storage === null;
    const rememberCandidates = (entries) => {
      for (const entry of entries) {
        candidates.set(entry.key, entry);
      }
    };
    const renderCandidatePanels = () => {
      const selected = synthesisSelectedKeys(model);
      for (const definition of relatedModeDefinitions) {
        const entries = suggestionEntries[definition.mode];
        if (entries === null) {
          continue;
        }
        renderSynthesisCandidates(
          view.suggestionResults[definition.mode],
          entries,
          selected,
          config.anchor.key,
          false,
          SUGGESTIONS_EMPTY
        );
      }
      if (searchEntries !== null) {
        renderSynthesisCandidates(
          view.searchResults,
          searchEntries,
          selected,
          config.anchor.key,
          true,
          SEARCH_EMPTY
        );
      }
    };
    const renderFieldErrors = () => {
      setSynthesisFieldError(view.titleError, showFieldErrors ? synthesisTitleError(model.title) : "");
      setSynthesisFieldError(
        view.folderError,
        showFieldErrors ? synthesisFolderError(model.folder) : ""
      );
      setSynthesisFieldError(view.draftError, showFieldErrors ? synthesisDraftError(model.draft) : "");
    };
    const render = () => {
      renderSynthesisSources(view, model, config);
      renderSynthesisContext(view, model);
      renderSynthesisCandidate(view, model);
      renderSynthesisDestination(view, model);
      renderSynthesisPreview(view, model);
      renderFieldErrors();
      view.copyButton.disabled = model.context === null || !hasWritableClipboard(clipboard);
      view.candidateCopyButton.disabled = !hasWritableClipboard(clipboard);
      view.generateButton.disabled = model.context === null || generating;
      view.previewButton.disabled = previewing || applying;
      view.applyButton.disabled = model.preview === null || applying;
    };
    const focusHandle = (key) => {
      (buttonByAttribute("data-synthesize-handle", key) || view.selectionStatus).focus({
        preventScroll: true
      });
    };
    const focusRemoveAt = (index) => {
      const source = model.sources[index] || model.sources[index - 1];
      const button = source ? buttonByAttribute("data-synthesize-remove", source.key) : null;
      (button || view.selectionStatus).focus({ preventScroll: true });
    };
    const addSource = (key) => {
      const candidate = candidates.get(key);
      if (!candidate) {
        announce("That note is no longer available to add.", "is-error");
        return;
      }
      const outcome = addSynthesisSource(model, config, candidate);
      announce(outcome.message, outcome.ok ? "is-success" : "is-error");
      if (!outcome.ok) {
        return;
      }
      render();
      renderCandidatePanels();
      persist();
    };
    const removeSource = (key) => {
      const index = model.sources.findIndex((source) => source.key === key);
      const outcome = removeSynthesisSource(model, key);
      announce(outcome.message, outcome.ok ? "is-success" : "is-error");
      if (!outcome.ok) {
        return;
      }
      render();
      renderCandidatePanels();
      persist();
      focusRemoveAt(index);
    };
    const moveSource = (key, direction) => {
      const outcome = moveSynthesisSource(model, key, direction);
      announce(outcome.message, outcome.ok ? "is-success" : "is-error");
      if (!outcome.ok) {
        return;
      }
      render();
      persist();
      focusHandle(key);
    };
    const applyDragOrder = (key) => {
      const keys = Array.from(
        view.sourceList.querySelectorAll("[data-synthesize-source]")
      ).filter((row) => !row.hasAttribute("data-synthesize-anchor")).map((row) => row.getAttribute("data-synthesize-source") || "");
      const moved = model.sources.find((source) => source.key === key);
      const outcome = reorderSynthesisSources(model, keys);
      render();
      if (outcome.ok) {
        persist();
        announce(
          `Moved ${moved?.title || key} to position ${synthesisSourcePosition(model, key)} of ${model.sources.length + 1}.`,
          "is-success"
        );
      }
      focusHandle(key);
    };
    const supporterRows = () => Array.from(view.sourceList.querySelectorAll("[data-synthesize-source]")).filter(
      (row) => !row.hasAttribute("data-synthesize-anchor")
    );
    const startDrag = (row, event) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      const key = row.getAttribute("data-synthesize-source") || "";
      const win = view.sourceList.ownerDocument.defaultView;
      if (!win) {
        return;
      }
      row.classList.add("is-dragging");
      const onPointerMove = (moveEvent) => {
        const next = supporterRows().find((other) => {
          if (other === row) {
            return false;
          }
          const rect = other.getBoundingClientRect();
          return moveEvent.clientY < rect.top + rect.height / 2;
        });
        if (next) {
          view.sourceList.insertBefore(row, next);
        } else {
          view.sourceList.appendChild(row);
        }
      };
      const finishDrag = () => {
        row.classList.remove("is-dragging");
        win.removeEventListener("pointermove", onPointerMove);
        win.removeEventListener("pointerup", finishDrag);
        win.removeEventListener("pointercancel", finishDrag);
        applyDragOrder(key);
      };
      win.addEventListener("pointermove", onPointerMove);
      win.addEventListener("pointerup", finishDrag, { once: true });
      win.addEventListener("pointercancel", finishDrag, { once: true });
    };
    const showSuggestionCount = (mode) => {
      const shown = view.suggestionResults[mode].querySelectorAll("[data-synthesize-candidate]").length;
      setStatusText(
        view.suggestionStatus,
        shown === 0 ? SUGGESTIONS_EMPTY : `${shown} ${shown === 1 ? "suggestion" : "suggestions"} in ${modeLabel(mode)}.`,
        shown === 0 ? "is-empty" : "is-success"
      );
    };
    const loadSuggestions = async (mode) => {
      const requestID = suggestionRequests[mode] + 1;
      suggestionRequests[mode] = requestID;
      view.suggestionPanels[mode].setAttribute("aria-busy", "true");
      if (mode === activeMode) {
        setStatusText(view.suggestionStatus, `Loading ${modeLabel(mode)} suggestions\u2026`, "is-loading");
      }
      try {
        const response = await requestSynthesisSuggestions(
          fetcher,
          config.relatedEndpoint,
          config.anchor.key,
          mode
        );
        if (suggestionRequests[mode] !== requestID) {
          return;
        }
        const entries = response.results.map(candidateFromRelatedResult);
        rememberCandidates(entries);
        suggestionEntries[mode] = entries;
        renderSynthesisCandidates(
          view.suggestionResults[mode],
          entries,
          synthesisSelectedKeys(model),
          config.anchor.key,
          false,
          SUGGESTIONS_EMPTY
        );
        if (mode === activeMode) {
          showSuggestionCount(mode);
        }
      } catch (error) {
        if (suggestionRequests[mode] !== requestID) {
          return;
        }
        if (mode === activeMode) {
          setStatusText(
            view.suggestionStatus,
            errorMessage2(error, "Suggestions could not be loaded."),
            "is-error"
          );
        }
      } finally {
        if (suggestionRequests[mode] === requestID) {
          view.suggestionPanels[mode].setAttribute("aria-busy", "false");
        }
      }
    };
    const selectMode = (mode) => {
      activeMode = mode;
      selectSynthesisSuggestionMode(view, mode);
      if (suggestionEntries[mode] === null) {
        return loadSuggestions(mode);
      }
      showSuggestionCount(mode);
      return Promise.resolve();
    };
    const runSearch = async (query) => {
      const trimmed = query.trim();
      if (trimmed === "") {
        searchRequest += 1;
        searchEntries = null;
        view.searchResults.replaceChildren();
        setStatusText(view.searchStatus, "Enter a search term to find notes.", "");
        return;
      }
      const requestID = searchRequest + 1;
      searchRequest = requestID;
      setStatusText(view.searchStatus, `Searching for ${trimmed}\u2026`, "is-loading");
      try {
        const results = await requestSynthesisSearch(fetcher, config.searchEndpoint, trimmed);
        if (searchRequest !== requestID) {
          return;
        }
        const entries = results.flatMap((result) => {
          const candidate = candidateFromSearchResult(result);
          return candidate ? [candidate] : [];
        });
        rememberCandidates(entries);
        searchEntries = entries;
        renderSynthesisCandidates(
          view.searchResults,
          entries,
          synthesisSelectedKeys(model),
          config.anchor.key,
          true,
          SEARCH_EMPTY
        );
        const shown = view.searchResults.querySelectorAll("[data-synthesize-candidate]").length;
        setStatusText(
          view.searchStatus,
          shown === 0 ? SEARCH_EMPTY : `${shown} ${shown === 1 ? "match" : "matches"} for ${trimmed}.`,
          shown === 0 ? "is-empty" : "is-success"
        );
      } catch (error) {
        if (searchRequest !== requestID) {
          return;
        }
        searchEntries = null;
        view.searchResults.replaceChildren();
        setStatusText(view.searchStatus, errorMessage2(error, "Search failed."), "is-error");
      }
    };
    const prepareContext = async () => {
      if (config.csrfToken === "") {
        setStatusText(view.contextStatus, "Context preparation is unavailable.", "is-error");
        return;
      }
      view.prepareButton.disabled = true;
      setStatusText(view.contextStatus, "Building context packet\u2026", "is-loading");
      const requested = synthesisContextSignature(model);
      const superseded = () => synthesisContextSignature(model) !== requested;
      try {
        const response = await requestSynthesisContext(fetcher, config.contextEndpoint, {
          source: config.anchor.key,
          candidates: synthesisSelectedKeys(model),
          purpose: model.purpose,
          prompt: model.prompt,
          token: config.csrfToken
        });
        if (superseded()) {
          setStatusText(view.contextStatus, CONTEXT_SUPERSEDED, "");
          return;
        }
        invalidateSynthesisPreview(model);
        model.context = response;
        model.contextStale = false;
        render();
        setStatusText(
          view.contextStatus,
          `Context ready. It expires at ${response.expires_at || "the server's timeout"}.`,
          "is-success"
        );
      } catch (error) {
        if (superseded()) {
          setStatusText(view.contextStatus, CONTEXT_SUPERSEDED, "");
          return;
        }
        invalidateSynthesisContext(model);
        model.contextStale = true;
        render();
        setStatusText(
          view.contextStatus,
          errorMessage2(error, "The context packet could not be built."),
          "is-error"
        );
      } finally {
        view.prepareButton.disabled = false;
      }
    };
    const copyContext = async () => {
      const context = model.context;
      if (!context) {
        setStatusText(view.contextStatus, "Prepare a context packet first.", "is-error");
        return;
      }
      if (!hasWritableClipboard(clipboard)) {
        setStatusText(
          view.contextStatus,
          "Clipboard unavailable. Select the packet Markdown below and copy it manually.",
          "is-error"
        );
        return;
      }
      view.copyButton.disabled = true;
      try {
        await clipboard.writeText(context.markdown);
        setStatusText(view.contextStatus, "Context packet copied.", "is-success");
      } catch {
        setStatusText(
          view.contextStatus,
          "Copy failed. Select the packet Markdown below and copy it manually.",
          "is-error"
        );
      } finally {
        view.copyButton.disabled = false;
      }
    };
    const forgetContext = (target) => {
      invalidateSynthesisContext(model);
      model.contextStale = true;
      render();
      setStatusText(target, CONTEXT_GONE, "is-error");
    };
    const generate = async () => {
      if (model.context === null) {
        setStatusText(view.generateStatus, "Prepare a context packet first.", "is-error");
        return;
      }
      if (generating) {
        return;
      }
      if (config.agentEnabled && !disclosureAccepted) {
        disclosureAccepted = confirmer(DISCLOSURE);
        if (!disclosureAccepted) {
          setStatusText(view.generateStatus, "Generation canceled.", "");
          return;
        }
      }
      generating = true;
      view.generateButton.disabled = true;
      view.candidatePanel.setAttribute("aria-busy", "true");
      setStatusText(view.generateStatus, "Generating a candidate draft\u2026", "is-loading");
      try {
        const response = await requestSynthesisGenerate(fetcher, config.generateEndpoint, {
          context_id: model.context.context_id,
          token: config.csrfToken
        });
        if (!response.enabled) {
          setStatusText(
            view.generateStatus,
            response.message || "Generation is unavailable until [agent].command is configured.",
            "is-empty"
          );
          return;
        }
        model.candidate = { markdown: response.output, html: response.rendered_html };
        render();
        persist();
        setStatusText(
          view.generateStatus,
          "Candidate ready. Your draft is unchanged until you replace or append it.",
          "is-success"
        );
        view.candidatePanel.focus({ preventScroll: true });
      } catch (error) {
        if (errorCode(error) === "not_found") {
          forgetContext(view.generateStatus);
          return;
        }
        setStatusText(
          view.generateStatus,
          errorMessage2(error, "The generation command failed."),
          "is-error"
        );
      } finally {
        generating = false;
        view.candidatePanel.setAttribute("aria-busy", "false");
        view.generateButton.disabled = model.context === null;
      }
    };
    const useCandidate = (mode) => {
      const candidate = model.candidate;
      if (candidate === null) {
        setStatusText(view.generateStatus, "There is no candidate to use.", "is-error");
        return;
      }
      const existing = model.draft.replace(/\s+$/, "");
      model.draft = mode === "replace" || existing === "" ? candidate.markdown : `${existing}

${candidate.markdown}`;
      view.draftInput.value = model.draft;
      invalidateSynthesisPreview(model);
      render();
      persist();
      setStatusText(
        view.generateStatus,
        mode === "replace" ? "Draft replaced with the candidate." : "Candidate appended to the draft.",
        "is-success"
      );
      view.draftInput.focus({ preventScroll: true });
    };
    const copyCandidate = async () => {
      const candidate = model.candidate;
      if (candidate === null) {
        setStatusText(view.generateStatus, "There is no candidate to copy.", "is-error");
        return;
      }
      if (!hasWritableClipboard(clipboard)) {
        setStatusText(
          view.generateStatus,
          "Clipboard unavailable. Select the candidate Markdown above and copy it manually.",
          "is-error"
        );
        return;
      }
      view.candidateCopyButton.disabled = true;
      try {
        await clipboard.writeText(candidate.markdown);
        setStatusText(view.generateStatus, "Candidate copied.", "is-success");
      } catch {
        setStatusText(
          view.generateStatus,
          "Copy failed. Select the candidate Markdown above and copy it manually.",
          "is-error"
        );
      } finally {
        view.candidateCopyButton.disabled = !hasWritableClipboard(clipboard);
      }
    };
    const dismissCandidate = () => {
      if (model.candidate === null) {
        return;
      }
      model.candidate = null;
      render();
      persist();
      setStatusText(view.generateStatus, "Candidate dismissed. Your draft is unchanged.", "");
      view.generateButton.focus({ preventScroll: true });
    };
    const focusInvalidField = () => {
      showFieldErrors = true;
      renderFieldErrors();
      const fields = [
        [synthesisTitleError(model.title), view.titleInput],
        [synthesisFolderError(model.folder), view.folderInput],
        [synthesisDraftError(model.draft), view.draftInput]
      ];
      const invalid = fields.find(([message]) => message !== "");
      if (!invalid) {
        return false;
      }
      invalid[1].focus({ preventScroll: true });
      setStatusText(view.previewStatus, invalid[0], "is-error");
      return true;
    };
    const preview = async () => {
      if (previewing || applying) {
        return;
      }
      if (model.context === null) {
        setStatusText(
          view.previewStatus,
          "Prepare a context packet before previewing the note.",
          "is-error"
        );
        return;
      }
      if (focusInvalidField()) {
        return;
      }
      previewing = true;
      render();
      view.previewPanel.setAttribute("aria-busy", "true");
      setStatusText(view.previewStatus, "Building the note preview\u2026", "is-loading");
      const requested = synthesisPreviewSignature(model);
      const superseded = () => synthesisPreviewSignature(model) !== requested;
      try {
        const response = await requestSynthesisPreview(fetcher, config.previewEndpoint, {
          context_id: model.context.context_id,
          title: model.title.trim(),
          folder: model.folder.trim(),
          draft: model.draft,
          token: config.csrfToken
        });
        if (superseded()) {
          setStatusText(view.previewStatus, PREVIEW_SUPERSEDED, "");
          return;
        }
        model.preview = response;
        model.previewStale = false;
        render();
        setStatusText(
          view.previewStatus,
          `Preview ready for ${response.destination}. Review it, then create the note.`,
          "is-success"
        );
        view.previewPanel.focus({ preventScroll: true });
      } catch (error) {
        if (superseded()) {
          setStatusText(view.previewStatus, PREVIEW_SUPERSEDED, "");
          return;
        }
        invalidateSynthesisPreview(model);
        if (errorCode(error) === "not_found") {
          forgetContext(view.previewStatus);
          return;
        }
        render();
        setStatusText(
          view.previewStatus,
          errorMessage2(error, "The note preview could not be built."),
          "is-error"
        );
      } finally {
        previewing = false;
        view.previewPanel.setAttribute("aria-busy", "false");
        render();
      }
    };
    const apply = async () => {
      const receipt = model.preview;
      if (receipt === null) {
        setStatusText(view.previewStatus, "Preview the note before creating it.", "is-error");
        return;
      }
      if (applying) {
        return;
      }
      applying = true;
      render();
      setStatusText(view.previewStatus, `Creating ${receipt.destination}\u2026`, "is-loading");
      try {
        const response = await requestSynthesisApply(fetcher, config.applyEndpoint, {
          preview_id: receipt.preview_id,
          token: config.csrfToken
        });
        const created = response.rel_path || receipt.destination;
        if (!clearSaved()) {
          invalidateSynthesisPreview(model);
          setSynthesisWarning(view, SAVED_STATE_STUCK, {
            href: response.path,
            label: `Open ${created}`
          });
          setStatusText(
            view.previewStatus,
            `Created ${created}. The saved copy of your work could not be deleted, so the note was not opened automatically.`,
            "is-success"
          );
          return;
        }
        setStatusText(view.previewStatus, `Created ${created}. Opening the note\u2026`, "is-success");
        redirect(response.path);
      } catch (error) {
        invalidateSynthesisPreview(model);
        render();
        setStatusText(
          view.previewStatus,
          `${errorMessage2(error, "The note could not be created.")} Preview again to retry; nothing you wrote was lost.`,
          "is-error"
        );
      } finally {
        applying = false;
        render();
      }
    };
    const hasMeaningfulWork = () => model.sources.length > 0 || model.title.trim() !== "" || model.prompt.trim() !== "" || model.draft.trim() !== "" || model.candidate !== null;
    const discard = () => {
      if (hasMeaningfulWork() && !confirmer(DISCARD_CONFIRMATION)) {
        announce("Kept the saved selection and draft.", "");
        return;
      }
      model.sources = [];
      model.title = "";
      model.folder = config.defaultFolder;
      model.purpose = config.defaultPurpose;
      model.prompt = "";
      model.draft = "";
      model.candidate = null;
      model.context = null;
      model.contextStale = false;
      model.preview = null;
      model.previewStale = false;
      showFieldErrors = false;
      view.titleInput.value = "";
      view.folderInput.value = model.folder;
      view.purposeSelect.value = model.purpose;
      view.promptInput.value = "";
      view.draftInput.value = "";
      const cleared = clearSaved();
      setSynthesisWarning(view, cleared ? "" : SAVED_STATE_STUCK);
      render();
      renderCandidatePanels();
      setStatusText(view.contextStatus, "", "");
      setStatusText(view.generateStatus, "", "");
      setStatusText(view.previewStatus, "", "");
      announce(
        cleared ? "Discarded the saved selection and draft." : "Cleared this page, but the saved copy could not be deleted.",
        cleared ? "is-success" : "is-error"
      );
    };
    const restore = async () => {
      const stored = readSynthesisState(storage, storageKey);
      if (!stored) {
        return;
      }
      model.title = stored.title;
      model.folder = stored.folder;
      model.purpose = config.purposes.includes(stored.purpose) ? stored.purpose : config.defaultPurpose;
      model.prompt = stored.prompt;
      model.draft = stored.draft;
      model.candidate = stored.candidate === "" ? null : { markdown: stored.candidate, html: "" };
      view.titleInput.value = model.title;
      view.folderInput.value = model.folder;
      view.purposeSelect.value = model.purpose;
      view.promptInput.value = model.prompt;
      view.draftInput.value = model.draft;
      const resolved = [];
      const missing = [];
      for (const key of stored.sources) {
        const source = await resolveSynthesisSource(fetcher, config.relatedEndpoint, key);
        if (source) {
          resolved.push(source);
        } else {
          missing.push(key);
        }
      }
      model.sources = dedupeSynthesisSources(resolved, config.anchor.key, config.maxSupporters);
      render();
      renderCandidatePanels();
      if (missing.length > 0) {
        setSynthesisWarning(
          view,
          `${missing.length} saved ${missing.length === 1 ? "note is" : "notes are"} no longer in this vault and ${missing.length === 1 ? "was" : "were"} removed from the selection: ${missing.join(", ")}.`
        );
      }
      persist();
    };
    view.sourceList.addEventListener("click", (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const remove = target?.closest("[data-synthesize-remove]");
      if (remove) {
        removeSource(remove.getAttribute("data-synthesize-remove") || "");
      }
    });
    view.sourceList.addEventListener("keydown", (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const handle = target?.closest("[data-synthesize-handle]");
      if (!handle || event.key !== "ArrowUp" && event.key !== "ArrowDown") {
        return;
      }
      event.preventDefault();
      moveSource(
        handle.getAttribute("data-synthesize-handle") || "",
        event.key === "ArrowUp" ? -1 : 1
      );
    });
    view.sourceList.addEventListener("pointerdown", (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const handle = target?.closest("[data-synthesize-handle]");
      const row = handle?.closest("[data-synthesize-source]");
      if (!row || row.hasAttribute("data-synthesize-anchor")) {
        return;
      }
      startDrag(row, event);
    });
    mount.addEventListener("click", (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const add = target?.closest("[data-synthesize-add]");
      if (!add || add.disabled) {
        return;
      }
      addSource(add.getAttribute("data-synthesize-add") || "");
    });
    for (const definition of relatedModeDefinitions) {
      const tab = view.suggestionTabs[definition.mode];
      tab.addEventListener("click", () => {
        void selectMode(definition.mode);
      });
      tab.addEventListener("keydown", (event) => {
        const index = relatedModeDefinitions.findIndex((item) => item.mode === definition.mode);
        let target = index;
        switch (event.key) {
          case "ArrowLeft":
            target = (index - 1 + relatedModeDefinitions.length) % relatedModeDefinitions.length;
            break;
          case "ArrowRight":
            target = (index + 1) % relatedModeDefinitions.length;
            break;
          default:
            return;
        }
        event.preventDefault();
        const next = relatedModeDefinitions[target].mode;
        view.suggestionTabs[next].focus();
        void selectMode(next);
      });
    }
    view.searchForm.addEventListener("submit", (event) => {
      event.preventDefault();
      void runSearch(view.searchInput.value);
    });
    view.titleInput.addEventListener("input", () => {
      model.title = view.titleInput.value;
      invalidateSynthesisPreview(model);
      render();
      persist();
    });
    view.folderInput.addEventListener("input", () => {
      model.folder = view.folderInput.value;
      invalidateSynthesisPreview(model);
      render();
      persist();
    });
    view.draftInput.addEventListener("input", () => {
      model.draft = view.draftInput.value;
      invalidateSynthesisPreview(model);
      render();
      persist();
    });
    view.purposeSelect.addEventListener("change", () => {
      model.purpose = view.purposeSelect.value;
      invalidateSynthesisContext(model);
      render();
      persist();
    });
    view.promptInput.addEventListener("input", () => {
      model.prompt = view.promptInput.value;
      invalidateSynthesisContext(model);
      render();
      persist();
    });
    view.prepareButton.addEventListener("click", () => {
      void prepareContext();
    });
    view.copyButton.addEventListener("click", () => {
      void copyContext();
    });
    view.discardButton.addEventListener("click", () => {
      discard();
    });
    view.generateButton.addEventListener("click", () => {
      void generate();
    });
    view.candidateReplaceButton.addEventListener("click", () => {
      useCandidate("replace");
    });
    view.candidateAppendButton.addEventListener("click", () => {
      useCandidate("append");
    });
    view.candidateCopyButton.addEventListener("click", () => {
      void copyCandidate();
    });
    view.candidateDismissButton.addEventListener("click", () => {
      dismissCandidate();
    });
    view.previewButton.addEventListener("click", () => {
      void preview();
    });
    view.applyButton.addEventListener("click", () => {
      void apply();
    });
    view.folderInput.value = model.folder;
    view.purposeSelect.value = model.purpose;
    render();
    const ready = (async () => {
      await restore();
      await selectMode("balanced");
    })();
    return { ready };
  };
  var initSynthesize = (deps = {}) => {
    const root = queryElement(document, "[data-synthesize-root]", HTMLElement);
    if (!root) {
      return null;
    }
    const mount = queryElement(root, "[data-synthesize-workbench]", HTMLElement);
    if (!mount || root.getAttribute("data-synthesize-anchor-key") === "") {
      return null;
    }
    return createSynthesisController(root, mount, deps);
  };

  // internal/web/static_src/main.ts
  (function() {
    initTheme();
    initCapture();
    initFacets();
    initOpenInEditor();
    initNotePinAction();
    initTrashNoteAction();
    initPinnedList();
    initNoteLayout();
    initNoteInsight();
    initFrontmatterEditing();
    initBrokenCreateActions();
    initCopyControls();
    initDailyBrief();
    initRelatedNotes();
    initGraph();
    initSearch();
    initOverviewSearchResults();
    initHistoryPanel();
    initDailyFixes();
    initProperties();
    initSynthesize();
  })();
})();
