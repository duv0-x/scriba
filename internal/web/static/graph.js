// Grafo de notas (PR4): simulación de fuerzas propia sobre <canvas>, sin dependencias.
// Se inicia solo en cada [data-grafo] (URL de datos); window.scribaGrafo(contenedor, url) lo expone.
(() => {
  if (window.scribaGrafo) return; // el script puede cargarse dos veces en la misma página
  const TIPOS = ["journal", "tickets", "decisions", "runbooks", "references", "postmortems"];
  const grupo = (t) => (TIPOS.includes(t) ? t : "otras");
  const still = matchMedia("(prefers-reduced-motion: reduce)");
  const href = (p) => "/n/" + p.split("/").map(encodeURIComponent).join("/");
  const MIN_ALPHA = 0.01;

  // Presencia: la reenvía live.js (única conexión SSE de la pestaña) como "scriba:presence".
  const writing = new Set();
  const graphs = [];
  let listening = false;
  function setPresence(list) {
    writing.clear();
    (list || []).forEach((a) => writing.add(a.path));
    graphs.forEach((g) => g.draw());
  }
  function listen() {
    if (listening) return;
    listening = true;
    addEventListener("scriba:presence", (e) => setPresence(e.detail));
    if (window.scribaPresence) setPresence(window.scribaPresence);
  }

  async function init(box, url) {
    const canvas = box.querySelector("canvas");
    const ctx = canvas.getContext("2d");
    const centro = box.dataset.centro || "";
    const local = !!centro;
    let data;
    try {
      const res = await fetch(url, { headers: { Accept: "application/json" } });
      if (!res.ok) throw new Error(res.status);
      data = await res.json();
    } catch {
      canvas.hidden = true;
      const c = box.querySelector("[data-gcount]");
      if (c) c.textContent = local ? "Sin enlaces aún" : "No se pudo cargar el grafo";
      return;
    }

    // Disposición inicial en filotaxis (determinista, sin solapes).
    const byId = new Map();
    const nodes = data.nodes.map((n, i) => {
      const a = i * 2.39996, r = 12 * Math.sqrt(i + 0.5);
      const node = { ...n, g: grupo(n.type), x: r * Math.cos(a), y: r * Math.sin(a), vx: 0, vy: 0, nb: new Set() };
      byId.set(n.id, node);
      return node;
    });
    const edges = data.edges.map((e) => ({ s: byId.get(e.source), t: byId.get(e.target) })).filter((e) => e.s && e.t);
    edges.forEach((e) => { e.s.nb.add(e.t); e.t.nb.add(e.s); });
    const degs = nodes.map((n) => n.degree).sort((a, b) => b - a);
    const hubMin = Math.max(1, degs[Math.floor(degs.length * 0.1)] || 0);
    const topMin = Math.max(hubMin, degs[Math.min(11, degs.length - 1)] || 0); // sin zoom, solo las ~12 notas con más enlaces

    const opts = { size: 1, width: 1, dist: local ? 55 : 120, orphans: local, types: new Set([...TIPOS, "otras"]) };
    const visible = (n) => opts.types.has(n.g) && (opts.orphans || n.degree > 0);
    const radius = (n) => (3 + Math.sqrt(n.degree) * 2) * opts.size * (n.id === centro ? 1.4 : 1);

    let vis = nodes.filter(visible), visEdges = [];
    const refilter = () => {
      vis = nodes.filter(visible);
      visEdges = edges.filter((e) => visible(e.s) && visible(e.t));
      const count = box.querySelector("[data-gcount]");
      if (count) count.textContent = local
        ? `Profundidad 1 · ${nodes.length - 1} ${nodes.length === 2 ? "nota conectada" : "notas conectadas"}`
        : `${vis.length} notas · ${visEdges.length} enlaces`;
    };
    refilter();

    // Simulación: repulsión O(n²) con corte, resortes en aristas, gravedad al centro y amortiguación.
    let alpha = 1, raf = 0, drag = null;
    function tick() {
      const L = opts.dist, rep = L * L * 0.6, cut2 = (L * 4) ** 2;
      for (let i = 0; i < vis.length; i++) {
        const a = vis[i];
        for (let j = i + 1; j < vis.length; j++) {
          const b = vis[j];
          let dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy;
          if (d2 > cut2) continue;
          if (d2 < 0.01) { dx = Math.random() - 0.5; dy = Math.random() - 0.5; d2 = 0.5; }
          const d = Math.sqrt(d2), f = (rep / Math.max(d2, (L / 3) ** 2)) * alpha; // piso: sin él, dos nodos casi encimados salen disparados
          a.vx += (dx / d) * f; a.vy += (dy / d) * f;
          b.vx -= (dx / d) * f; b.vy -= (dy / d) * f;
        }
      }
      for (const e of visEdges) {
        const dx = e.t.x - e.s.x, dy = e.t.y - e.s.y, d = Math.sqrt(dx * dx + dy * dy) || 1;
        const f = ((d - L) / d) * 0.06 * alpha;
        e.s.vx += dx * f; e.s.vy += dy * f;
        e.t.vx -= dx * f; e.t.vy -= dy * f;
      }
      for (const n of vis) {
        n.vx = (n.vx - n.x * 0.004 * alpha) * 0.6;
        n.vy = (n.vy - n.y * 0.004 * alpha) * 0.6;
        if (n === drag) { n.vx = n.vy = 0; continue; }
        const v = Math.hypot(n.vx, n.vy), vmax = 20;
        if (v > vmax) { n.vx *= vmax / v; n.vy *= vmax / v; }
        n.x += n.vx; n.y += n.vy;
      }
      alpha *= 0.985;
    }
    function frame() {
      raf = 0;
      tick();
      if (!touched) fit();
      draw();
      if (alpha > MIN_ALPHA) raf = requestAnimationFrame(frame);
    }
    // Sin animación (reduced motion) se simula igual, pero en lotes cortos para no congelar la pestaña.
    let batch = 0;
    function settle() {
      batch = 0;
      for (let i = 0; i < 15 && alpha > MIN_ALPHA; i++) tick();
      if (alpha > MIN_ALPHA) { batch = setTimeout(settle, 0); return; }
      if (!touched) fit();
      draw();
    }
    function run(heat) {
      alpha = Math.max(alpha, heat);
      if (still.matches) { if (!batch) batch = setTimeout(settle, 0); }
      else if (!raf) raf = requestAnimationFrame(frame);
    }

    // Vista: pantalla = mundo * k + (ox, oy), en px CSS.
    let k = 1, ox = 0, oy = 0, touched = false, W = 0, H = 0;
    function fit() {
      if (!vis.length || !W) return;
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
      for (const n of vis) {
        x0 = Math.min(x0, n.x); y0 = Math.min(y0, n.y); x1 = Math.max(x1, n.x); y1 = Math.max(y1, n.y);
      }
      const pad = local ? 28 : 60, padX = local ? 64 : pad; // el local deja sitio a las etiquetas a los lados
      k = Math.min((W - 2 * padX) / (x1 - x0 || 1), (H - 2 * pad) / (y1 - y0 || 1), local ? 1.6 : 2);
      ox = W / 2 - ((x0 + x1) / 2) * k;
      oy = H / 2 - ((y0 + y1) / 2) * k;
    }
    function zoom(f, px = W / 2, py = H / 2) {
      const nk = Math.min(8, Math.max(0.1, k * f));
      ox = px - ((px - ox) * nk) / k;
      oy = py - ((py - oy) * nk) / k;
      k = nk;
      touched = true;
      draw();
    }
    function resize() {
      const dpr = devicePixelRatio || 1;
      W = canvas.clientWidth; H = canvas.clientHeight;
      canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
      if (!touched) fit();
      draw();
    }

    // Colores del tema actual, leídos de las variables CSS.
    let col = {};
    function readColors() {
      const cs = getComputedStyle(box), v = (n) => cs.getPropertyValue(n).trim();
      col = { accent: v("--accent"), scribe: v("--scribe"), ink: v("--ink"), muted: v("--muted"), paper: v("--paper") };
      for (const t of [...TIPOS, "otras"]) col[t] = v("--g-" + t) || col.muted;
    }

    let hover = null;
    function draw() {
      if (!W) return;
      const dpr = devicePixelRatio || 1;
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.setTransform(dpr * k, 0, 0, dpr * k, dpr * ox, dpr * oy);
      const hl = drag || hover;
      const lit = (n) => !hl || n === hl || hl.nb.has(n);

      ctx.lineWidth = opts.width / k;
      for (const e of visEdges) {
        const on = hl && (e.s === hl || e.t === hl);
        ctx.globalAlpha = on ? 1 : hl ? 0.3 * 0.35 : 0.35;
        ctx.strokeStyle = on ? col.accent : col.muted;
        ctx.beginPath(); ctx.moveTo(e.s.x, e.s.y); ctx.lineTo(e.t.x, e.t.y); ctx.stroke();
      }
      for (const n of vis) {
        const r = radius(n);
        ctx.globalAlpha = lit(n) ? 1 : 0.3;
        if (n.id === centro) {
          ctx.fillStyle = col.accent; ctx.globalAlpha *= 0.25;
          ctx.beginPath(); ctx.arc(n.x, n.y, r + 7 / k, 0, 7); ctx.fill();
          ctx.globalAlpha = lit(n) ? 1 : 0.3;
        }
        ctx.fillStyle = n.id === centro || n === hl ? col.accent : col[n.g];
        ctx.beginPath(); ctx.arc(n.x, n.y, r, 0, 7); ctx.fill();
        if (writing.has(n.id)) {
          ctx.strokeStyle = col.scribe; ctx.lineWidth = 2 / k;
          ctx.beginPath(); ctx.arc(n.x, n.y, r + 4 / k, 0, 7); ctx.stroke();
          ctx.lineWidth = opts.width / k;
        }
      }
      ctx.font = `500 ${(local ? 10 : 12) / k}px "IBM Plex Sans", system-ui, sans-serif`;
      ctx.textAlign = "center";
      ctx.textBaseline = "top";
      for (const n of vis) {
        if (!(local || n === hl || n.degree >= (k > 1.2 ? hubMin : topMin))) continue;
        ctx.globalAlpha = lit(n) ? 1 : 0.3;
        ctx.fillStyle = col.ink;
        const max = local ? 24 : 40, t = n.title.length > max ? n.title.slice(0, max - 1) + "…" : n.title;
        ctx.fillText(t, n.x, n.y + radius(n) + 4 / k);
      }
      ctx.globalAlpha = 1;
    }

    // Tooltip (solo en la vista completa: título, path mono y enlaces).
    const tip = box.querySelector(".gtip");
    function showTip(n, px, py) {
      if (!tip) return;
      if (!n) { tip.hidden = true; return; }
      const b = document.createElement("strong"), c = document.createElement("code"), s = document.createElement("small");
      b.textContent = n.title;
      c.textContent = n.id;
      s.textContent = n.degree === 1 ? "1 enlace" : `${n.degree} enlaces`;
      tip.replaceChildren(b, c, s);
      tip.hidden = false;
      tip.style.left = Math.min(px + 14, W - tip.offsetWidth - 8) + "px";
      tip.style.top = Math.min(py + 14, H - tip.offsetHeight - 8) + "px";
    }

    // Interacción: arrastrar un nodo lo mueve, el fondo hace pan, clic abre la nota, rueda hace zoom.
    const at = (e) => { const r = canvas.getBoundingClientRect(); return [e.clientX - r.left, e.clientY - r.top]; };
    function hit(px, py) {
      const x = (px - ox) / k, y = (py - oy) / k;
      for (let i = vis.length - 1; i >= 0; i--) {
        const n = vis[i], r = radius(n) + 4 / k;
        if ((n.x - x) ** 2 + (n.y - y) ** 2 <= r * r) return n;
      }
      return null;
    }
    let down = null;
    canvas.addEventListener("pointerdown", (e) => {
      const [px, py] = at(e);
      const n = hit(px, py);
      down = { n, px, py, ox, oy, moved: false };
      drag = n;
      canvas.setPointerCapture(e.pointerId);
    });
    canvas.addEventListener("pointermove", (e) => {
      const [px, py] = at(e);
      if (down) {
        if (Math.abs(px - down.px) + Math.abs(py - down.py) > 3) down.moved = true;
        if (!down.moved) return;
        if (down.n) {
          down.n.x = (px - ox) / k; down.n.y = (py - oy) / k;
          touched = true;
          showTip(null);
          if (!still.matches) run(0.3); // sin animación: solo se mueve el nodo
          draw();
        } else {
          ox = down.ox + px - down.px; oy = down.oy + py - down.py;
          touched = true;
          draw();
        }
        return;
      }
      const n = hit(px, py);
      canvas.style.cursor = n ? "pointer" : "grab";
      showTip(n, px, py);
      if (n !== hover) { hover = n; draw(); }
    });
    canvas.addEventListener("pointerup", () => {
      if (down && !down.moved && down.n) location.href = href(down.n.id);
      down = null; drag = null;
      draw();
    });
    canvas.addEventListener("pointerleave", () => {
      if (down) return;
      hover = null; showTip(null); draw();
    });
    if (!local) {
      canvas.addEventListener("wheel", (e) => {
        e.preventDefault();
        const [px, py] = at(e);
        zoom(Math.exp(-e.deltaY * 0.0015), px, py);
      }, { passive: false });
    }
    box.querySelectorAll("[data-z]").forEach((b) => b.addEventListener("click", () => {
      if (b.dataset.z === "fit") { touched = false; fit(); draw(); } else zoom(b.dataset.z === "in" ? 1.25 : 0.8);
    }));

    // Filtros: tipos y huérfanas recalientan la simulación; tamaño y grosor solo repintan.
    const form = box.querySelector("form.gfilters");
    form?.addEventListener("input", (e) => {
      const f = form.elements;
      opts.types = new Set([...form.querySelectorAll('input[name="tipo"]:checked')].map((c) => c.value));
      opts.orphans = f.huerfanas.checked;
      opts.size = +f.tam.value; opts.width = +f.grosor.value;
      const out = e.target.closest("label")?.querySelector("output");
      if (out) out.textContent = out.dataset.unit ? (+e.target.value).toFixed(1) + out.dataset.unit : e.target.value;
      if (e.target.name === "tam" || e.target.name === "grosor") { draw(); return; }
      if (e.target.name === "dist") return; // se re-simula al soltar el slider ("change"), no en cada paso
      refilter();
      touched = false;
      run(0.6);
    });
    form?.addEventListener("change", (e) => {
      if (e.target.name !== "dist") return;
      opts.dist = +e.target.value;
      touched = false;
      run(0.6);
    });

    // Tema: repinta si cambia data-theme o el esquema del sistema.
    const recolor = () => { readColors(); draw(); };
    const mo = new MutationObserver(recolor), dark = matchMedia("(prefers-color-scheme: dark)"), ro = new ResizeObserver(resize);
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    dark.addEventListener("change", recolor);

    readColors();
    ro.observe(canvas);
    const stop = () => { cancelAnimationFrame(raf); mo.disconnect(); ro.disconnect(); dark.removeEventListener("change", recolor); };
    graphs.push({ box, draw, stop });
    listen();
    run(1);
  }

  // Tras una navegación parcial (ADR-009) suelta los grafos que salieron del DOM e inicia los nuevos.
  function scan() {
    for (let i = graphs.length - 1; i >= 0; i--) if (!graphs[i].box.isConnected) graphs.splice(i, 1)[0].stop();
    document.querySelectorAll("[data-grafo]:not([data-ready])").forEach((b) => {
      b.dataset.ready = "";
      init(b, b.dataset.grafo);
    });
  }
  window.scribaGrafo = init;
  document.addEventListener("htmx:after:settle", scan);
  scan();
})();
