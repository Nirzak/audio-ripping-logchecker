document.addEventListener("DOMContentLoaded", function () {

  /* ---- Theme Toggle ---- */
  const toggle = document.getElementById("theme-toggle");
  const html = document.documentElement;
  const savedTheme = localStorage.getItem("logchecker-theme");

  if (savedTheme) {
    html.setAttribute("data-theme", savedTheme);
  } else if (window.matchMedia("(prefers-color-scheme: light)").matches) {
    html.setAttribute("data-theme", "light");
  }

  toggle.addEventListener("click", function () {
    const current = html.getAttribute("data-theme");
    const next = current === "dark" ? "light" : "dark";
    html.setAttribute("data-theme", next);
    localStorage.setItem("logchecker-theme", next);
  });

  /* ---- File Input UX ---- */
  const dropZone = document.getElementById("file-drop-zone");
  const fileInput = document.getElementById("logfile");
  const fileNameEl = document.getElementById("file-selected-name");

  if (fileInput && fileNameEl) {
    fileInput.addEventListener("change", function () {
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
      dropZone.addEventListener(evt, function (e) {
        e.preventDefault();
        e.stopPropagation();
        dropZone.classList.add("dragover");
      });
    });
    dropZone.addEventListener("dragleave", function (e) {
      e.preventDefault();
      e.stopPropagation();
      dropZone.classList.remove("dragover");
    });
    dropZone.addEventListener("drop", function (e) {
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

  /* ---- Load Result ---- */
  const outputContainer = document.getElementById("output-container");
  // resultSrcdoc holds the fetched HTML so the modal can reuse it without
  // re-fetching and without any innerHTML assignment on the main container.
  let resultSrcdoc = null;

  if (outputContainer) {
    const resultId = outputContainer.getAttribute("data-result-id");
    const rawSubpath = outputContainer.getAttribute("data-subpath") || '';
    const subpath = rawSubpath.replace(/\/+$/, '');
    const resultUrl = (subpath ? subpath : '') + "/result/" + resultId;

    fetch(resultUrl)
      .then(response => response.text())
      .then(data => {
        resultSrcdoc = data;
        // Render inside a sandboxed iframe to avoid any innerHTML-based XSS.
        // allow-same-origin is needed so the iframe can inherit stylesheets
        // served from the same origin (style.css referenced in the result doc).
        const frame = document.createElement("iframe");
        frame.setAttribute("sandbox", "allow-same-origin");
        frame.setAttribute("title", "Log analysis output");
        frame.style.cssText = "width:100%;border:none;min-height:400px;background:transparent;display:block;";
        frame.srcdoc = data;
        outputContainer.innerHTML = "";
        outputContainer.appendChild(frame);
      })
      .catch(() => {
        const errSpan = document.createElement("span");
        errSpan.style.color = "var(--danger-text)";
        errSpan.textContent = "Error loading output. Please try again.";
        outputContainer.innerHTML = "";
        outputContainer.appendChild(errSpan);
      });
  }

  /* ---- Log Modal ---- */
  const btnExpand = document.getElementById("btn-expand");
  const logModal = document.getElementById("log-modal");
  const logModalBackdrop = document.getElementById("log-modal-backdrop");
  const logModalBody = document.getElementById("log-modal-body");
  const btnModalClose = document.getElementById("btn-modal-close");

  function openLogModal() {
    if (resultSrcdoc && logModalBody) {
      // Reuse the already-fetched srcdoc — no innerHTML assignment.
      logModalBody.innerHTML = "";
      const frame = document.createElement("iframe");
      frame.setAttribute("sandbox", "allow-same-origin");
      frame.setAttribute("title", "Log analysis output (expanded)");
      frame.style.cssText = "width:100%;height:100%;border:none;background:transparent;display:block;";
      frame.srcdoc = resultSrcdoc;
      logModalBody.appendChild(frame);
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
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && logModal && logModal.classList.contains("open")) {
      closeLogModal();
    }
  });

});
