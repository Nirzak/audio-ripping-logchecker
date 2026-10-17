document.addEventListener("DOMContentLoaded", function() {

  /* ---- Theme Toggle ---- */
  const toggle = document.getElementById("theme-toggle");
  const html = document.documentElement;
  const savedTheme = localStorage.getItem("logchecker-theme");

  if (savedTheme) {
    html.setAttribute("data-theme", savedTheme);
  } else if (window.matchMedia("(prefers-color-scheme: light)").matches) {
    html.setAttribute("data-theme", "light");
  }

  if (toggle) {
    toggle.addEventListener("click", function() {
      const current = html.getAttribute("data-theme");
      const next = current === "dark" ? "light" : "dark";
      html.setAttribute("data-theme", next);
      localStorage.setItem("logchecker-theme", next);
    });
  }

  /* ---- File Input UX ---- */
  const dropZone = document.getElementById("file-drop-zone");
  const fileInput = document.getElementById("logfile");
  const fileNameEl = document.getElementById("file-selected-name");

  if (fileInput && fileNameEl) {
    fileInput.addEventListener("change", function() {
      if (fileInput.files.length > 0) {
        fileNameEl.textContent = "\u{1F4C4} " + fileInput.files[0].name;
        fileNameEl.classList.add("visible");
      } else {
        fileNameEl.classList.remove("visible");
      }
    });
  }

  if (dropZone) {
    ["dragenter", "dragover"].forEach(evt => {
      dropZone.addEventListener(evt, function(e) {
        e.preventDefault();
        e.stopPropagation();
        dropZone.classList.add("dragover");
      });
    });
    dropZone.addEventListener("dragleave", function(e) {
      e.preventDefault();
      e.stopPropagation();
      dropZone.classList.remove("dragover");
    });
    dropZone.addEventListener("drop", function(e) {
      e.preventDefault();
      e.stopPropagation();
      dropZone.classList.remove("dragover");
      if (e.dataTransfer.files.length > 0) {
        fileInput.files = e.dataTransfer.files;
        fileNameEl.textContent = "\u{1F4C4} " + e.dataTransfer.files[0].name;
        fileNameEl.classList.add("visible");
      }
    });
  }

  /* ---- Input method: upload a file or paste text ----
     Only the active input is enabled, so exactly one of "logfile" / "logtext"
     is submitted. Without JS the tabs stay hidden and the form is a plain file
     upload. The pasted text is only ever sent to the server as form data; it is
     never inserted into this page as HTML. */
  const MAX_LOG_BYTES = 200 * 1024;
  const MODE_KEY = "logchecker-input-mode";
  const DRAFT_KEY = "logchecker-paste-draft";
  const uploadForm = document.getElementById("upload-form");
  const inputTabs = document.getElementById("input-tabs");
  const pasteZone = document.getElementById("paste-zone");
  const logText = document.getElementById("logtext");
  const pasteCount = document.getElementById("paste-count");
  const pasteClear = document.getElementById("paste-clear");
  const btnAnalyze = document.getElementById("btn-analyze");
  const inputTabsReady = !!(uploadForm && inputTabs && dropZone && fileInput && pasteZone && logText && pasteCount && pasteClear);
  const tabs = inputTabsReady ? Array.from(inputTabs.querySelectorAll('[role="tab"]')) : [];
  const pasteHint = pasteCount ? pasteCount.textContent : "";
  let mode = "file";

  // Storage can throw (private mode, blocked site data); it is only a convenience.
  function storageGet(store, key) {
    try { return store.getItem(key); } catch (e) { return null; }
  }
  function storageSet(store, key, value) {
    try {
      if (value === null) store.removeItem(key); else store.setItem(key, value);
    } catch (e) { /* noop */ }
  }

  // Size as the server receives it: browsers submit textarea newlines as CRLF.
  function submittedBytes(text) {
    return new TextEncoder().encode(text.replace(/\r?\n/g, "\r\n")).length;
  }

  function formatSize(bytes) {
    if (bytes < 1024) return bytes + " B";
    const kb = bytes / 1024;
    return (kb < 10 ? kb.toFixed(1) : Math.round(kb)) + " KB";
  }

  function updatePasteMeta() {
    const text = logText.value;
    let over = false;
    if (text === "") {
      pasteCount.textContent = pasteHint;
    } else {
      const bytes = submittedBytes(text);
      const lines = text.split(/\r\n|\r|\n/).length;
      over = bytes > MAX_LOG_BYTES;
      pasteCount.textContent = lines.toLocaleString() + (lines === 1 ? " line" : " lines") +
        " \u2022 " + formatSize(bytes) + " / 200 KB" + (over ? " \u2014 too large" : "");
    }
    pasteZone.classList.toggle("over", over);
    pasteClear.hidden = text === "";
    if (btnAnalyze) btnAnalyze.disabled = over;
  }

  function setMode(next) {
    mode = next === "text" ? "text" : "file";
    const isText = mode === "text";
    tabs.forEach(tab => {
      const selected = tab.dataset.mode === mode;
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
    });
    dropZone.hidden = isText;
    pasteZone.hidden = !isText;
    fileInput.disabled = isText;
    fileInput.required = !isText;
    logText.disabled = !isText;
    logText.required = isText;
    if (fileNameEl) fileNameEl.classList.toggle("visible", !isText && fileInput.files.length > 0);
    if (isText) {
      updatePasteMeta();
    } else if (btnAnalyze) {
      btnAnalyze.disabled = false;
    }
    storageSet(localStorage, MODE_KEY, mode);
  }

  function setFile(file) {
    const dt = new DataTransfer();
    dt.items.add(file);
    fileInput.files = dt.files;
    setMode("file");
    fileInput.dispatchEvent(new Event("change"));
  }

  function setText(text) {
    setMode("text");
    logText.value = text;
    updatePasteMeta();
    logText.focus();
    logText.setSelectionRange(0, 0);
    logText.scrollTop = 0;
  }

  if (inputTabsReady) {
    inputTabs.hidden = false;
    dropZone.setAttribute("role", "tabpanel");
    dropZone.setAttribute("aria-labelledby", "tab-file");

    // A failed paste submission re-renders the page with an error; restore the
    // text so it isn't lost. The draft is dropped on every load either way.
    const draft = storageGet(sessionStorage, DRAFT_KEY);
    storageSet(sessionStorage, DRAFT_KEY, null);
    if (draft && document.getElementById("error-alert")) {
      logText.value = draft;
      setMode("text");
    } else {
      setMode(storageGet(localStorage, MODE_KEY));
    }

    tabs.forEach((tab, i) => {
      tab.addEventListener("click", () => {
        setMode(tab.dataset.mode);
        if (mode === "text") logText.focus();
      });
      // Arrow / Home / End move between tabs (WAI-ARIA tabs pattern).
      tab.addEventListener("keydown", e => {
        let j = -1;
        if (e.key === "ArrowRight") j = (i + 1) % tabs.length;
        else if (e.key === "ArrowLeft") j = (i - 1 + tabs.length) % tabs.length;
        else if (e.key === "Home") j = 0;
        else if (e.key === "End") j = tabs.length - 1;
        if (j === -1) return;
        e.preventDefault();
        setMode(tabs[j].dataset.mode);
        tabs[j].focus();
      });
    });

    logText.addEventListener("input", updatePasteMeta);

    pasteClear.addEventListener("click", () => {
      logText.value = "";
      updatePasteMeta();
      logText.focus();
    });

    // Dropping a file on the paste box switches to file upload instead of
    // letting the browser open the file.
    pasteZone.addEventListener("dragover", e => {
      if (e.dataTransfer && Array.from(e.dataTransfer.types).includes("Files")) e.preventDefault();
    });
    pasteZone.addEventListener("drop", e => {
      if (!e.dataTransfer || e.dataTransfer.files.length === 0) return;
      e.preventDefault();
      setFile(e.dataTransfer.files[0]);
    });

    // Paste anywhere on the page (outside a text field) to fill the form:
    // a copied .log/.txt file selects it, copied text goes to the paste box.
    document.addEventListener("paste", e => {
      const t = e.target;
      if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || (t && t.isContentEditable)) return;
      if (document.querySelector(".log-modal.open")) return;
      const dt = e.clipboardData;
      if (!dt) return;
      const logFile = Array.from(dt.files).find(f => /\.(log|txt)$/i.test(f.name));
      if (logFile) {
        e.preventDefault();
        setFile(logFile);
        return;
      }
      const text = dt.getData("text/plain");
      if (text.trim() !== "") {
        e.preventDefault();
        setText(text);
      }
    });

    uploadForm.addEventListener("submit", e => {
      if (mode !== "text") return;
      if (submittedBytes(logText.value) > MAX_LOG_BYTES) {
        e.preventDefault();
        updatePasteMeta();
        return;
      }
      storageSet(sessionStorage, DRAFT_KEY, logText.value);
    });
  }

  /* ---- Score Ring ----
     Score is rendered server-side as a hidden summary item; relocate its value
     into the sidebar ring. The gradient fill shows the earned portion; a red
     "deducted" ring underneath reveals itself as points are lost. At 0 or
     below the ring is fully red. */
  (function initScoreRing() {
    const scoreEl = document.querySelector('#summary-grid [data-key="score"] .summary-value');
    const numEl = document.getElementById("score-number");
    const ring = document.getElementById("score-ring-fill");
    const redRing = document.getElementById("score-ring-deducted");
    if (!scoreEl || !numEl || !ring) return;

    const s = parseInt(scoreEl.textContent.trim(), 10);
    if (Number.isNaN(s)) return;

    const labelEl = document.getElementById("score-label");
    const descEl = document.getElementById("score-desc");
    const pct = Math.max(0, Math.min(100, s));

    numEl.textContent = s;
    ring.style.setProperty("--score-pct", pct);

    // Show red deducted ring when score is below 100
    if (redRing && s < 100) {
      redRing.style.strokeDashoffset = "0"; // full red circle behind gradient
    }

    if (s <= 0) {
      ring.classList.add("neg");
      numEl.classList.add("neg");
    }
    if (s === 100) {
      if (labelEl) labelEl.textContent = "Perfect Rip!";
      if (descEl) descEl.textContent = "This extraction is verified and accurate.";
    } else {
      if (labelEl) labelEl.textContent = "";
      if (descEl) descEl.textContent = "";
    }
  })();

  /* ---- Log viewer: line numbers, search, word-wrap ---- */
  const outputContainer = document.getElementById("output-container");

  // Build line-numbered rows from a fetched log HTML document string.
  // Handles multi-line <span> tags (e.g. XLD signature wrapped in a single
  // <span class='good'>) by carrying open spans across line boundaries.
  function buildLogLines(container, docString) {
    let body = docString;
    try {
      const doc = new DOMParser().parseFromString(docString, "text/html");
      const pre = doc.querySelector("pre");
      if (pre) body = pre.innerHTML;
    } catch (e) { /* fall back to raw string */ }

    const lines = body.replace(/\n$/, "").split("\n");
    const frag = document.createDocumentFragment();
    const tagRe = /<(\/?)(span)([^>]*)>/gi;
    let openSpans = []; // stack of opening span tag strings carried forward

    lines.forEach((line, i) => {
      // Prefix: re-open any spans still open from previous lines
      const prefix = openSpans.join("");

      // Scan the *original* line (without prefix) to update the span stack
      const nextOpenSpans = openSpans.slice(); // copy current state
      let m;
      tagRe.lastIndex = 0;
      while ((m = tagRe.exec(line)) !== null) {
        if (m[1] === "/") {
          // closing tag — pops the most recent open span
          if (nextOpenSpans.length > 0) nextOpenSpans.pop();
        } else {
          // opening tag — push the full tag string
          nextOpenSpans.push(m[0]);
        }
      }

      // Suffix: close any spans still open so this line is self-contained HTML
      const suffix = "</span>".repeat(nextOpenSpans.length);
      const fullLine = prefix + line + suffix;

      const row = document.createElement("div");
      row.className = "log-line";
      const ln = document.createElement("span");
      ln.className = "ln";
      ln.textContent = i + 1;
      const lc = document.createElement("span");
      lc.className = "lc";
      lc.innerHTML = fullLine === "" ? "&nbsp;" : fullLine;
      lc._orig = lc.innerHTML;
      row.appendChild(ln);
      row.appendChild(lc);
      frag.appendChild(row);

      // Carry forward open spans to the next line
      openSpans = nextOpenSpans;
    });
    container.innerHTML = "";
    container.appendChild(frag);
  }

  if (outputContainer) {
    const resultId = outputContainer.getAttribute("data-result-id");
    const rawSubpath = outputContainer.getAttribute("data-subpath") || '';
    const subpath = rawSubpath.replace(/\/+$/, '');
    const resultUrl = (subpath ? subpath : '') + "/result/" + resultId;

    fetch(resultUrl)
      .then(response => response.text())
      .then(data => {
        buildLogLines(outputContainer, data);
        applyWrap();
      })
      .catch(error => {
        outputContainer.innerHTML = '<span style="color:var(--danger-text)">Error loading output. Please try again.</span>';
      });
  }

  /* ---- Word-wrap toggle ---- */
  const wrapToggle = document.getElementById("wrap-toggle");
  const modalBodyEl = document.getElementById("log-modal-body");

  function applyWrap() {
    const nowrap = wrapToggle && !wrapToggle.checked;
    if (outputContainer) outputContainer.classList.toggle("nowrap", nowrap);
    if (modalBodyEl) modalBodyEl.classList.toggle("nowrap", nowrap);
  }
  if (wrapToggle) {
    wrapToggle.addEventListener("change", applyWrap);
    applyWrap();
  }

  /* ---- Search in report ---- */
  const searchInput = document.getElementById("log-search");

  function escapeRegExp(str) {
    return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  // Wrap matches of `re` in <mark> within text nodes only (preserves coloring spans).
  // Built with DOM nodes, never innerHTML: a text node's value is decoded log
  // text, so "&lt;img&gt;" in the log would otherwise become a live <img>.
  function highlightLine(lc, re) {
    const walker = document.createTreeWalker(lc, NodeFilter.SHOW_TEXT, null);
    const targets = [];
    let node;
    while ((node = walker.nextNode())) {
      re.lastIndex = 0;
      if (re.test(node.nodeValue)) targets.push(node);
    }
    targets.forEach(textNode => {
      const text = textNode.nodeValue;
      const frag = document.createDocumentFragment();
      let last = 0;
      let m;
      re.lastIndex = 0;
      while ((m = re.exec(text)) !== null) {
        frag.appendChild(document.createTextNode(text.slice(last, m.index)));
        const mark = document.createElement("mark");
        mark.textContent = m[0];
        frag.appendChild(mark);
        last = m.index + m[0].length;
      }
      frag.appendChild(document.createTextNode(text.slice(last)));
      textNode.parentNode.replaceChild(frag, textNode);
    });
  }

  if (searchInput && outputContainer) {
    searchInput.addEventListener("input", function() {
      const q = searchInput.value.trim();
      const rows = outputContainer.querySelectorAll(".log-line");
      if (!q) {
        rows.forEach(row => {
          row.classList.remove("dim");
          const lc = row.querySelector(".lc");
          if (lc && lc._orig != null) lc.innerHTML = lc._orig;
        });
        return;
      }
      const re = new RegExp(escapeRegExp(q), "gi");
      rows.forEach(row => {
        const lc = row.querySelector(".lc");
        if (!lc) return;
        lc.innerHTML = lc._orig;            // clear previous marks
        re.lastIndex = 0;
        const matches = re.test(lc.textContent);
        if (matches) {
          row.classList.remove("dim");
          re.lastIndex = 0;
          highlightLine(lc, re);
        } else {
          row.classList.add("dim");
        }
      });
    });
  }

  /* ---- Copy disc IDs ---- */
  document.querySelectorAll(".discid-copy").forEach(btn => {
    btn.addEventListener("click", function() {
      const val = btn.getAttribute("data-copy") || "";
      const done = () => {
        btn.classList.add("copied");
        setTimeout(() => btn.classList.remove("copied"), 1200);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(val).then(done).catch(() => {});
      } else {
        const ta = document.createElement("textarea");
        ta.value = val;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand("copy"); done(); } catch (e) { /* noop */ }
        document.body.removeChild(ta);
      }
    });
  });

  /* ---- Log Modal ---- */
  const btnExpand = document.getElementById("btn-expand");
  const logModal = document.getElementById("log-modal");
  const logModalBackdrop = document.getElementById("log-modal-backdrop");
  const logModalBody = document.getElementById("log-modal-body");
  const btnModalClose = document.getElementById("btn-modal-close");

  function openLogModal() {
    if (outputContainer && logModalBody) {
      logModalBody.innerHTML = outputContainer.innerHTML;
      logModalBody.classList.toggle("nowrap", outputContainer.classList.contains("nowrap"));
    }
    logModal.classList.add("open");
    logModalBackdrop.classList.add("open");
    document.body.style.overflow = "hidden";
  }

  function closeLogModal() {
    logModal.classList.remove("open");
    logModalBackdrop.classList.remove("open");
    document.body.style.overflow = "";
  }

  if (btnExpand) {
    btnExpand.addEventListener("click", openLogModal);
  }
  if (btnModalClose) {
    btnModalClose.addEventListener("click", closeLogModal);
  }
  if (logModalBackdrop) {
    logModalBackdrop.addEventListener("click", closeLogModal);
  }
  document.addEventListener("keydown", function(e) {
    if (e.key === "Escape" && logModal && logModal.classList.contains("open")) {
      closeLogModal();
    }
  });

});
