// Tiempo real (PR3b): presencia de agentes, puntos "sin leer" en Folios y reproducción en vivo
// de la nota abierta. Escucha GET /events (SSE); EventSource reconecta solo.
(() => {
  if (!window.EventSource) return;
  const badge = document.getElementById("live");
  const note = document.querySelector("[data-note]")?.dataset.note;
  const still = matchMedia("(prefers-reduced-motion: reduce)");
  const links = (p) => document.querySelectorAll(`.tree a[data-path="${CSS.escape(p)}"]`);
  const href = (p) => "/n/" + p.split("/").map(encodeURIComponent).join("/");

  function presence(list) {
    const paths = new Set(list.map((a) => a.path));
    document.querySelectorAll(".tree a.writing").forEach((a) => paths.has(a.dataset.path) || a.classList.remove("writing"));
    paths.forEach((p) => links(p).forEach((a) => a.classList.add("writing")));
    if (!badge) return;
    badge.hidden = list.length === 0;
    badge.replaceChildren();
    if (!list.length) return;
    const a = list[0];
    const av = document.createElement("span");
    av.className = "avatar";
    av.setAttribute("aria-hidden", "true");
    av.textContent = a.agent.charAt(0).toUpperCase();
    const code = document.createElement("code");
    code.textContent = a.path;
    const more = list.length > 1 ? ` (+${list.length - 1})` : "";
    badge.append(av, `${a.agent} editando `, code, more);
    badge.href = href(a.path);
  }

  // Reproducción: revela los bloques .chg carácter a carácter con el caret cobre al final.
  let run = 0;
  function play(prose) {
    const id = ++run;
    const nodes = [];
    prose.querySelectorAll(".chg").forEach((c) => {
      const w = document.createTreeWalker(c, NodeFilter.SHOW_TEXT);
      while (w.nextNode()) nodes.push({ n: w.currentNode, t: w.currentNode.data });
    });
    if (still.matches || !nodes.length) return;
    nodes.forEach((x) => (x.n.data = ""));
    const caret = document.createElement("span");
    caret.className = "caret";
    let i = 0, k = 0;
    const step = () => {
      if (id !== run) return nodes.slice(i).forEach((x) => (x.n.data = x.t));
      let budget = 2 + Math.floor(Math.random() * 3);
      while (budget > 0 && i < nodes.length) {
        const x = nodes[i], take = Math.min(budget, x.t.length - k);
        k += take;
        budget -= take;
        x.n.data = x.t.slice(0, k);
        x.n.after(caret);
        if (k >= x.t.length) i++, k = 0;
      }
      if (i < nodes.length) requestAnimationFrame(step);
      else caret.remove();
    };
    requestAnimationFrame(step);
  }

  let busy = false, again = false;
  async function refresh() {
    if (busy) return void (again = true);
    busy = true;
    try {
      const res = await fetch(location.pathname + "?fragmento=1", { headers: { Accept: "text/html" } });
      const prose = document.querySelector(".prose");
      if (res.ok && prose) {
        run++; // corta la reproducción anterior antes de reemplazar el DOM
        prose.innerHTML = await res.text();
        play(prose);
      }
    } catch (_) {
      // sin red: el próximo evento lo vuelve a intentar
    } finally {
      busy = false;
      if (again) (again = false), refresh();
    }
  }

  // La nota abierta se borró: aviso en lugar de pedir un fragmento que ya no existe.
  function gone() {
    run++;
    const prose = document.querySelector(".prose");
    if (!prose || prose.previousElementSibling?.matches(".alert")) return;
    const p = document.createElement("p");
    p.className = "alert";
    p.setAttribute("role", "alert");
    p.textContent = "Esta nota se borró";
    prose.before(p);
  }

  const es = new EventSource("/events");
  es.addEventListener("presence", (e) => presence(JSON.parse(e.data) || []));
  es.addEventListener("changed", (e) => {
    const ev = JSON.parse(e.data);
    if (ev.kind === "deleted") {
      links(ev.path).forEach((a) => a.classList.remove("unread", "writing"));
      if (ev.path === note) gone();
    } else if (ev.path === note) refresh();
    else links(ev.path).forEach((a) => a.classList.add("unread"));
  });
})();
