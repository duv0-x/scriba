// Editor web: vista previa con debounce, Ctrl/⌘+S para guardar y aviso al salir con cambios.
(() => {
  const form = document.getElementById("editor");
  if (!form) return;
  const src = form.elements.content;
  const preview = document.getElementById("preview");
  let dirty = form.hasAttribute("data-dirty");
  let timer, ctrl;

  src.addEventListener("input", () => {
    dirty = true;
    clearTimeout(timer);
    timer = setTimeout(async () => {
      ctrl?.abort();
      ctrl = new AbortController();
      const body = new URLSearchParams({ content: src.value, path: form.dataset.path });
      try {
        const res = await fetch("/vista-previa", { method: "POST", body, signal: ctrl.signal });
        if (res.ok) preview.innerHTML = await res.text();
      } catch (e) {
        if (e.name !== "AbortError") console.warn("vista previa:", e);
      }
    }, 300);
  });

  form.addEventListener("submit", () => { dirty = false; });
  addEventListener("beforeunload", (e) => { if (dirty) e.preventDefault(); });
  addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      form.requestSubmit();
    }
  });
})();
