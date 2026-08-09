(() => {
  const state = { file: null, result: null, maxSizeMb: 50 };
  const $ = (id) => document.getElementById(id);
  const dropzone = $("dropzone");
  const fileInput = $("file-input");
  const extractButton = $("extract-button");
  const errorBox = $("error-box");

  // Keep the MCP endpoint aligned with the host, scheme and port used to open
  // the WebUI, including deployments behind a domain name or reverse proxy.
  $("mcp-url").textContent = new URL("/mcp", window.location.origin).href;

  const formatBytes = (bytes) => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
  };

  const showError = (message) => {
    errorBox.textContent = message;
    errorBox.hidden = false;
  };

  const clearError = () => {
    errorBox.hidden = true;
    errorBox.textContent = "";
  };

  const isPDF = (file) => file
    && (file.name.toLowerCase().endsWith(".pdf") || file.type === "application/pdf");

  const pastedPDF = (clipboardData) => {
    for (const file of Array.from(clipboardData?.files || [])) {
      if (isPDF(file)) return file;
    }
    for (const item of Array.from(clipboardData?.items || [])) {
      if (item.kind !== "file") continue;
      const file = item.getAsFile();
      if (isPDF(file)) return file;
    }
    return null;
  };

  const selectFile = (file) => {
    clearError();
    if (!file) return;
    if (!isPDF(file)) {
      showError("请选择 PDF 文件。");
      return;
    }
    if (file.size > state.maxSizeMb * 1024 * 1024) {
      showError(`文件超过 ${state.maxSizeMb} MB 限制。`);
      return;
    }
    state.file = file;
    $("empty-file-state").hidden = true;
    $("selected-file-state").hidden = false;
    $("file-name").textContent = file.name;
    $("file-meta").textContent = `${formatBytes(file.size)} · 点击可重新选择`;
    extractButton.disabled = false;
  };

  const setLoading = (loading) => {
    extractButton.disabled = loading || !state.file;
    extractButton.querySelector(".button-label").hidden = loading;
    extractButton.querySelector(".button-progress").hidden = !loading;
  };

  dropzone.addEventListener("click", () => fileInput.click());
  dropzone.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      fileInput.click();
    }
  });
  fileInput.addEventListener("change", () => selectFile(fileInput.files[0]));
  document.addEventListener("paste", (event) => {
    const file = pastedPDF(event.clipboardData);
    if (!file) return;
    event.preventDefault();
    selectFile(file);
  });

  ["dragenter", "dragover"].forEach((name) => {
    dropzone.addEventListener(name, (event) => {
      event.preventDefault();
      dropzone.classList.add("dragging");
    });
  });
  ["dragleave", "drop"].forEach((name) => {
    dropzone.addEventListener(name, (event) => {
      event.preventDefault();
      dropzone.classList.remove("dragging");
    });
  });
  dropzone.addEventListener("drop", (event) => selectFile(event.dataTransfer.files[0]));

  extractButton.addEventListener("click", async () => {
    if (!state.file) return;
    clearError();
    const firstPage = $("first-page").value;
    const lastPage = $("last-page").value;
    if (firstPage && lastPage && Number(firstPage) > Number(lastPage)) {
      showError("起始页不能大于结束页。");
      return;
    }

    const form = new FormData();
    form.append("file", state.file);
    form.append("layout", $("layout").checked ? "true" : "false");
    form.append("page_breaks", $("page-breaks").checked ? "true" : "false");
    if (firstPage) form.append("first_page", firstPage);
    if (lastPage) form.append("last_page", lastPage);
    if ($("password").value) form.append("password", $("password").value);

    setLoading(true);
    try {
      const response = await fetch("/api/v1/extract", { method: "POST", body: form });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload.error?.message || `提取失败（HTTP ${response.status}）`);
      }
      state.result = payload;
      $("result-text").value = payload.text;
      $("result-meta").textContent = `${payload.filename} · ${payload.character_count.toLocaleString()} 字符 · ${payload.elapsed_ms} ms`;
      $("result-panel").hidden = false;
      $("result-panel").scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (error) {
      showError(error.message || "无法连接到提取服务。");
    } finally {
      setLoading(false);
    }
  });

  $("copy-button").addEventListener("click", async (event) => {
    if (!state.result) return;
    await navigator.clipboard.writeText(state.result.text);
    const button = event.currentTarget;
    button.textContent = "已复制";
    window.setTimeout(() => { button.textContent = "复制文本"; }, 1400);
  });

  $("download-button").addEventListener("click", () => {
    if (!state.result) return;
    const blob = new Blob([state.result.text], { type: "text/plain;charset=utf-8" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = state.result.filename.replace(/\.pdf$/i, "") + ".txt";
    link.click();
    URL.revokeObjectURL(link.href);
  });

  fetch("/api/v1/info")
    .then((response) => response.json())
    .then((info) => {
      state.maxSizeMb = info.max_file_size_mb;
      $("file-limit").textContent = `支持 PDF · 最大 ${state.maxSizeMb} MB`;
    })
    .catch(() => {});
})();
