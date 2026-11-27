// Extensión "Live Preview" para CodeMirror 6 (ADR-006): la línea del cursor
// muestra el Markdown en crudo y el resto se ve renderizado mediante
// decoraciones. Nunca modifica el documento salvo el clic en casillas de tarea.
import { StateField } from "@codemirror/state";
import { EditorView, Decoration, WidgetType, keymap } from "@codemirror/view";
import {
  syntaxTree,
  foldService,
  codeFolding,
  foldKeymap,
  foldedRanges,
  foldEffect,
  unfoldEffect,
} from "@codemirror/language";

// Configuración de la instancia (la página monta un solo editor).
let conf = { links: {}, base: "" };

// --- Utilidades puras (exportadas para pruebas) ---

// slug igual que mdparse.Slug del servidor.
export function slug(s) {
  let out = "";
  let guion = false;
  for (const ch of s.toLowerCase()) {
    if (/[\p{L}\p{N}]/u.test(ch)) {
      if (guion && out) out += "-";
      guion = false;
      out += ch;
    } else if (ch === "-" || /\s/u.test(ch)) {
      guion = true;
    }
  }
  return out;
}

// Parte el interior de un wikilink y lo resuelve contra el mapa de enlaces.
export function parseWikilink(inner, links, base) {
  const pi = inner.indexOf("|");
  const dest = pi >= 0 ? inner.slice(0, pi) : inner;
  const alias = pi >= 0 ? inner.slice(pi + 1) : "";
  const text = alias || dest;
  const hi = dest.indexOf("#");
  const target = (hi >= 0 ? dest.slice(0, hi) : dest).trim();
  const frag = hi >= 0 ? dest.slice(hi + 1) : "";
  if (target === "") {
    if (frag) return { text, href: "#" + slug(frag), missing: false };
    return { text, href: null, missing: true };
  }
  const dst = links[target];
  if (dst) {
    let href = base + "/n/" + dst.split("/").map(encodeURIComponent).join("/");
    if (frag) href += "#" + encodeURIComponent(slug(frag));
    return { text, href, missing: false };
  }
  return { text, href: null, missing: true };
}

// Convierte las líneas crudas de una tabla GFM en estructura.
export function parseTable(lines) {
  const celdas = (linea) => {
    let t = linea.trim();
    if (t.startsWith("|")) t = t.slice(1);
    if (t.endsWith("|")) t = t.slice(0, -1);
    const out = [];
    let cur = "";
    for (let i = 0; i < t.length; i++) {
      const ch = t[i];
      if (ch === "\\" && t[i + 1] === "|") {
        cur += "|";
        i++;
      } else if (ch === "|") {
        out.push(cur.trim());
        cur = "";
      } else {
        cur += ch;
      }
    }
    out.push(cur.trim());
    return out;
  };
  // Quita marcas envolventes simples (**, *, ~~, `) del texto de una celda.
  const limpia = (s) =>
    s
      .replace(/^\*\*(.*)\*\*$/, "$1")
      .replace(/^~~(.*)~~$/, "$1")
      .replace(/^\*(.*)\*$/, "$1")
      .replace(/^`(.*)`$/, "$1");
  const align = celdas(lines[1] || "").map((d) =>
    d.startsWith(":") && d.endsWith(":") ? "center" : d.endsWith(":") ? "right" : "left"
  );
  const head = celdas(lines[0] || "").map(limpia);
  const rows = lines.slice(2).map((l) => celdas(l).map(limpia));
  return { align, head, rows };
}

// Índice del fin de la línea de cierre del frontmatter, o -1 si no hay.
export function frontmatterEnd(text) {
  if (!text.startsWith("---\n")) return -1;
  let pos = 4;
  while (pos <= text.length) {
    let fin = text.indexOf("\n", pos);
    if (fin < 0) fin = text.length;
    const linea = text.slice(pos, fin);
    if (linea === "---" || linea === "...") return fin;
    pos = fin + 1;
  }
  return -1;
}

// --- Widgets ---

class WidgetImg extends WidgetType {
  constructor(src, alt) {
    super();
    this.src = src;
    this.alt = alt;
  }
  eq(o) {
    return o.src === this.src && o.alt === this.alt;
  }
  toDOM() {
    const img = document.createElement("img");
    img.className = "cm-lp-img";
    img.alt = this.alt;
    img.src = this.src;
    img.loading = "lazy";
    return img;
  }
}

class WidgetBullet extends WidgetType {
  eq() {
    return true;
  }
  toDOM() {
    const s = document.createElement("span");
    s.className = "cm-lp-bullet";
    s.setAttribute("aria-hidden", "true");
    s.textContent = "•";
    return s;
  }
}

class WidgetHR extends WidgetType {
  eq() {
    return true;
  }
  toDOM() {
    const s = document.createElement("span");
    s.className = "cm-lp-hr";
    s.setAttribute("role", "separator");
    return s;
  }
}

class WidgetTarea extends WidgetType {
  // offset: distancia desde el inicio del reemplazo al carácter dentro de [ ]
  constructor(marcada, offset) {
    super();
    this.marcada = marcada;
    this.offset = offset;
  }
  eq(o) {
    return o.marcada === this.marcada && o.offset === this.offset;
  }
  toDOM(view) {
    const input = document.createElement("input");
    input.type = "checkbox";
    input.className = "cm-lp-task";
    input.checked = this.marcada;
    input.setAttribute("aria-label", "Tarea");
    input.addEventListener("mousedown", (e) => e.preventDefault());
    input.addEventListener("click", () => {
      if (view.state.readOnly) {
        input.checked = this.marcada;
        return;
      }
      const desde = view.posAtDOM(input) + this.offset;
      view.dispatch({
        changes: { from: desde, to: desde + 1, insert: this.marcada ? " " : "x" },
      });
    });
    return input;
  }
  ignoreEvent() {
    return true;
  }
}

// Navegación parcial con htmx dentro del lector; recarga completa si no hay htmx.
function navegar(href) {
  if (window.htmx) {
    const cont = document.querySelector(".reader") || document.body;
    const a = document.createElement("a");
    a.href = href;
    a.style.display = "none";
    cont.appendChild(a);
    window.htmx.process(a);
    a.click();
    a.remove();
  } else {
    location.href = href;
  }
}

class WidgetWiki extends WidgetType {
  constructor(texto, href, faltante) {
    super();
    this.texto = texto;
    this.href = href;
    this.faltante = faltante;
  }
  eq(o) {
    return o.texto === this.texto && o.href === this.href && o.faltante === this.faltante;
  }
  toDOM() {
    const a = document.createElement("a");
    a.textContent = this.texto;
    if (this.faltante) {
      a.className = "wl missing";
      a.title = "Nota aún no escrita";
    } else {
      a.className = "wl";
      a.href = this.href;
      a.addEventListener("mousedown", (e) => e.preventDefault());
      a.addEventListener("click", (e) => {
        if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return; // nativo
        e.preventDefault();
        navegar(this.href);
      });
    }
    return a;
  }
  ignoreEvent() {
    return !this.faltante; // el inexistente deja que CM ponga el cursor
  }
}

// Base de los bloques reemplazados (fence y tabla): un clic revela el crudo.
class WidgetBloque extends WidgetType {
  constructor(fuente) {
    super();
    this.fuente = fuente;
  }
  eq(o) {
    return o.fuente === this.fuente;
  }
  envolver(dom, view) {
    dom.classList.add("cm-lp-block");
    dom.addEventListener("mousedown", (e) => {
      e.preventDefault();
      // Posición calculada en el momento del clic: el DOM puede ser reutilizado
      // por eq() aunque el bloque se haya desplazado en el documento.
      view.dispatch({ selection: { anchor: view.posAtDOM(dom) } });
      view.focus();
    });
    return dom;
  }
  ignoreEvent() {
    return true;
  }
}

class WidgetFence extends WidgetBloque {
  constructor(fuente, contenido, lang) {
    super(fuente);
    this.contenido = contenido;
    this.lang = lang;
  }
  eq(o) {
    return o.fuente === this.fuente && o.lang === this.lang;
  }
  toDOM(view) {
    const div = document.createElement("div");
    const pre = document.createElement("pre");
    if (this.lang) pre.dataset.lang = this.lang;
    const code = document.createElement("code");
    code.textContent = this.contenido;
    pre.appendChild(code);
    div.appendChild(pre);
    return this.envolver(div, view);
  }
}

class WidgetTabla extends WidgetBloque {
  toDOM(view) {
    const { align, head, rows } = parseTable(this.fuente.split("\n"));
    const div = document.createElement("div");
    const tabla = document.createElement("table");
    const thead = document.createElement("thead");
    const trh = document.createElement("tr");
    head.forEach((txt, i) => {
      const th = document.createElement("th");
      th.textContent = txt;
      if (align[i] && align[i] !== "left") th.style.textAlign = align[i];
      trh.appendChild(th);
    });
    thead.appendChild(trh);
    tabla.appendChild(thead);
    const tbody = document.createElement("tbody");
    for (const fila of rows) {
      const tr = document.createElement("tr");
      fila.forEach((txt, i) => {
        const td = document.createElement("td");
        td.textContent = txt;
        if (align[i] && align[i] !== "left") td.style.textAlign = align[i];
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    }
    tabla.appendChild(tbody);
    div.appendChild(tabla);
    return this.envolver(div, view);
  }
}

class WidgetFold extends WidgetType {
  constructor(plegado, desde, hasta) {
    super();
    this.plegado = plegado;
    this.desde = desde;
    this.hasta = hasta;
  }
  eq(o) {
    return o.plegado === this.plegado && o.desde === this.desde && o.hasta === this.hasta;
  }
  toDOM(view) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "cm-lp-fold";
    b.setAttribute("aria-expanded", String(!this.plegado));
    b.setAttribute("aria-label", this.plegado ? "Desplegar propiedades" : "Plegar propiedades");
    b.textContent = this.plegado ? "▸" : "▾";
    b.addEventListener("mousedown", (e) => e.preventDefault());
    b.addEventListener("click", () => {
      view.dispatch({
        effects: (this.plegado ? unfoldEffect : foldEffect).of({ from: this.desde, to: this.hasta }),
      });
    });
    return b;
  }
  ignoreEvent() {
    return true;
  }
}

// --- Construcción de decoraciones ---

const RE_WIKI = /!?\[\[([^\[\]\n]+?)\]\]/g;

// ¿Hay algún pliegue dentro del rango del frontmatter?
function estaPlegado(state, desde, hasta) {
  try {
    let p = false;
    foldedRanges(state).between(desde, hasta, () => {
      p = true;
    });
    return p;
  } catch {
    return false; // el campo de plegado aún no está inicializado
  }
}

function construirDecoraciones(state) {
  const doc = state.doc;
  const decos = [];
  const marca = Decoration.mark({ class: "cm-lp-mark" });
  const oculto = Decoration.replace({});

  // Líneas tocadas por cualquier rango de la selección.
  const activas = new Set();
  for (const r of state.selection.ranges) {
    const a = doc.lineAt(r.from).number;
    const b = doc.lineAt(r.to).number;
    for (let i = a; i <= b; i++) activas.add(i);
  }
  const lineaActiva = (pos) => activas.has(doc.lineAt(pos).number);
  // Un bloque se revela si la selección se solapa con él (inclusive).
  const bloqueRevelado = (n) =>
    state.selection.ranges.some((r) => r.from <= n.to && r.to >= n.from);

  const fmFin = frontmatterEnd(doc.toString());
  const dentroFm = (n) => fmFin >= 0 && n.to <= fmFin;

  if (fmFin >= 0) {
    const ultima = doc.lineAt(fmFin);
    for (let i = 1; i <= ultima.number; i++)
      decos.push(Decoration.line({ class: "cm-lp-fm" }).range(doc.line(i).from));
    const rango = { from: doc.line(1).to, to: ultima.from - 1 };
    decos.push(
      Decoration.widget({
        widget: new WidgetFold(estaPlegado(state, rango.from, rango.to), rango.from, rango.to),
        side: -1,
      }).range(0)
    );
  }

  const arbol = syntaxTree(state);

  // Primera pasada: rangos donde no se buscan wikilinks.
  const excluidos = [];
  arbol.iterate({
    enter(n) {
      if (dentroFm(n)) return false;
      if (n.name === "FencedCode" || n.name === "CodeBlock" || n.name === "InlineCode") {
        excluidos.push({ from: n.from, to: n.to });
        return false;
      }
    },
  });

  // Wikilinks (Lezer no los parsea), línea a línea.
  const wikilinks = [];
  for (let ln = 1; ln <= doc.lines; ln++) {
    const linea = doc.line(ln);
    if (fmFin >= 0 && linea.from < fmFin) continue;
    RE_WIKI.lastIndex = 0;
    let m;
    while ((m = RE_WIKI.exec(linea.text))) {
      const from = linea.from + m.index;
      const to = from + m[0].length;
      if (excluidos.some((r) => from < r.to && to > r.from)) continue;
      wikilinks.push({ from, to, inner: m[1], ln });
    }
  }
  const enWikilink = (n) => wikilinks.some((w) => n.from >= w.from && n.to <= w.to);

  // Segunda pasada: decoraciones del árbol de sintaxis.
  arbol.iterate({
    enter(n) {
      if (dentroFm(n) || enWikilink(n)) return false;
      const nombre = n.name;

      if (nombre.startsWith("ATXHeading")) {
        decos.push(
          Decoration.line({ class: "cm-lp-h" + nombre.slice(-1) }).range(doc.lineAt(n.from).from)
        );
        const activa = lineaActiva(n.from);
        for (const m of n.node.getChildren("HeaderMark")) {
          if (activa) {
            decos.push(marca.range(m.from, m.to));
          } else {
            const hasta = doc.sliceString(m.to, m.to + 1) === " " ? m.to + 1 : m.to;
            decos.push(oculto.range(m.from, hasta));
          }
        }
        return; // el contenido del título sigue procesándose
      }

      switch (nombre) {
        case "StrongEmphasis":
          decos.push(Decoration.mark({ class: "cm-lp-strong" }).range(n.from, n.to));
          break;
        case "Emphasis":
          decos.push(Decoration.mark({ class: "cm-lp-em" }).range(n.from, n.to));
          break;
        case "Strikethrough":
          decos.push(Decoration.mark({ class: "cm-lp-strike" }).range(n.from, n.to));
          break;
        case "EmphasisMark":
        case "StrikethroughMark":
          decos.push((lineaActiva(n.from) ? marca : oculto).range(n.from, n.to));
          break;

        case "InlineCode": {
          const activa = lineaActiva(n.from);
          const marcas = n.node.getChildren("CodeMark");
          if (marcas.length >= 2 && marcas[0].to < marcas[marcas.length - 1].from)
            decos.push(
              Decoration.mark({ class: "cm-lp-code-inline" }).range(
                marcas[0].to,
                marcas[marcas.length - 1].from
              )
            );
          for (const m of marcas) decos.push((activa ? marca : oculto).range(m.from, m.to));
          return false;
        }

        case "Link": {
          const activa = lineaActiva(n.from);
          const marcas = n.node.getChildren("LinkMark");
          if (!marcas.length) break;
          const cierre = marcas.length > 1 ? marcas[1] : null;
          const textoFin = cierre ? cierre.from : n.to;
          if (marcas[0].to < textoFin)
            decos.push(Decoration.mark({ class: "cm-lp-link" }).range(marcas[0].to, textoFin));
          if (activa) {
            for (const m of marcas) decos.push(marca.range(m.from, m.to));
            for (const extra of ["URL", "LinkTitle"]) {
              const x = n.node.getChild(extra);
              if (x) decos.push(marca.range(x.from, x.to));
            }
          } else {
            decos.push(oculto.range(marcas[0].from, marcas[0].to));
            if (cierre) decos.push(oculto.range(cierre.from, n.to)); // ]( url "título" )
          }
          return; // énfasis dentro del texto del enlace sigue procesándose
        }

        case "Image": {
          if (lineaActiva(n.from)) {
            decos.push(marca.range(n.from, n.to));
          } else {
            const marcas = n.node.getChildren("LinkMark");
            const url = n.node.getChild("URL");
            const alt = marcas.length > 1 ? doc.sliceString(marcas[0].to, marcas[1].from) : "";
            const src = url ? doc.sliceString(url.from, url.to) : "";
            decos.push(
              Decoration.replace({ widget: new WidgetImg(src, alt) }).range(n.from, n.to)
            );
          }
          return false;
        }

        case "ListMark": {
          const item = n.node.parent;
          const lista = item && item.parent;
          if (
            lista &&
            lista.name === "BulletList" &&
            !item.getChild("Task") &&
            !lineaActiva(n.from)
          )
            decos.push(Decoration.replace({ widget: new WidgetBullet() }).range(n.from, n.from + 1));
          break;
        }

        case "Task": {
          const item = n.node.parent;
          const lm = item && item.getChild("ListMark");
          const tk = n.node.getChild("TaskMarker");
          if (lm && tk && !lineaActiva(n.from)) {
            const marcada = /x/i.test(doc.sliceString(tk.from + 1, tk.to - 1));
            decos.push(
              Decoration.replace({ widget: new WidgetTarea(marcada, tk.from + 1 - lm.from) }).range(
                lm.from,
                tk.to
              )
            );
          }
          return false;
        }

        case "Blockquote": {
          const a = doc.lineAt(n.from).number;
          const b = doc.lineAt(n.to).number;
          for (let i = a; i <= b; i++)
            decos.push(Decoration.line({ class: "cm-lp-quote" }).range(doc.line(i).from));
          return; // contenido y QuoteMark siguen procesándose
        }

        case "QuoteMark": {
          if (lineaActiva(n.from)) {
            decos.push(marca.range(n.from, n.to));
          } else {
            const hasta = doc.sliceString(n.to, n.to + 1) === " " ? n.to + 1 : n.to;
            decos.push(oculto.range(n.from, hasta));
          }
          break;
        }

        case "HorizontalRule":
          if (!lineaActiva(n.from))
            decos.push(Decoration.replace({ widget: new WidgetHR() }).range(n.from, n.to));
          break;

        case "FencedCode":
        case "Table": {
          if (bloqueRevelado(n)) {
            const cls = nombre === "FencedCode" ? "cm-lp-code" : "cm-lp-table";
            const a = doc.lineAt(n.from).number;
            const b = doc.lineAt(n.to).number;
            for (let i = a; i <= b; i++)
              decos.push(Decoration.line({ class: cls }).range(doc.line(i).from));
          } else {
            const ini = doc.lineAt(n.from).from;
            const fin = doc.lineAt(n.to).to;
            const fuente = doc.sliceString(n.from, n.to);
            let widget;
            if (nombre === "FencedCode") {
              const ct = n.node.getChild("CodeText");
              const ci = n.node.getChild("CodeInfo");
              widget = new WidgetFence(
                fuente,
                ct ? doc.sliceString(ct.from, ct.to) : "",
                ci ? doc.sliceString(ci.from, ci.to) : ""
              );
            } else {
              widget = new WidgetTabla(fuente);
            }
            decos.push(Decoration.replace({ block: true, widget }).range(ini, fin));
          }
          return false;
        }
      }
    },
  });

  // Decoraciones de wikilinks.
  for (const w of wikilinks) {
    if (activas.has(w.ln)) {
      decos.push(Decoration.mark({ class: "cm-lp-wikilink" }).range(w.from, w.to));
    } else {
      const r = parseWikilink(w.inner, conf.links, conf.base);
      decos.push(
        Decoration.replace({ widget: new WidgetWiki(r.text, r.href, r.missing) }).range(w.from, w.to)
      );
    }
  }

  return Decoration.set(decos, true);
}

// --- Campo de estado y extensión ---

export const lpField = StateField.define({
  create(state) {
    return construirDecoraciones(state);
  },
  update(valor, tr) {
    if (
      tr.docChanged ||
      tr.selection ||
      syntaxTree(tr.startState) !== syntaxTree(tr.state) ||
      tr.effects.some((e) => e.is(foldEffect) || e.is(unfoldEffect))
    )
      return construirDecoraciones(tr.state);
    return valor;
  },
  provide: (f) => EditorView.decorations.from(f),
});

// Plegado del frontmatter: se ofrece solo en la línea 1 cuando hay frontmatter.
const plegarFrontmatter = foldService.of((state, from) => {
  const fmFin = frontmatterEnd(state.doc.toString());
  if (fmFin < 0 || state.doc.lineAt(from).number !== 1) return null;
  return { from: state.doc.line(1).to, to: state.doc.lineAt(fmFin).from - 1 };
});

export function livePreview({ links, base }) {
  conf = { links: links || {}, base: base || "" };
  return [plegarFrontmatter, codeFolding(), keymap.of(foldKeymap), lpField];
}
