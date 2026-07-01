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
  function buildLogLines(container, docString) {
    let body = docString;
    try {
      const doc = new DOMParser().parseFromString(docString, "text/html");
      const pre = doc.querySelector("pre");
      if (pre) body = pre.innerHTML;
    } catch (e) { /* fall back to raw string */ }

    const lines = body.replace(/\n$/, "").split("\n");
    const frag = document.createDocumentFragment();
    lines.forEach((line, i) => {
      const row = document.createElement("div");
      row.className = "log-line";
      const ln = document.createElement("span");
      ln.className = "ln";
      ln.textContent = i + 1;
      const lc = document.createElement("span");
      lc.className = "lc";
      lc.innerHTML = line === "" ? "&nbsp;" : line;
      lc._orig = lc.innerHTML;
      row.appendChild(ln);
      row.appendChild(lc);
      frag.appendChild(row);
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
  function highlightLine(lc, re) {
    const walker = document.createTreeWalker(lc, NodeFilter.SHOW_TEXT, null);
    const targets = [];
    let node;
    while ((node = walker.nextNode())) {
      re.lastIndex = 0;
      if (re.test(node.nodeValue)) targets.push(node);
    }
    targets.forEach(textNode => {
      re.lastIndex = 0;
      const span = document.createElement("span");
      span.innerHTML = textNode.nodeValue.replace(re, m => "<mark>" + m + "</mark>");
      textNode.parentNode.replaceChild(span, textNode);
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
