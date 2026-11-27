// Editor en línea del lector: CodeMirror 6 sobre la nota, con autosave.
// Se monta tras cada navegación parcial vía window.scribaInline.mount().
import { EditorState, Compartment, Annotation, Prec, Transaction } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { history, historyKeymap, defaultKeymap, indentWithTab } from "@codemirror/commands";
import { markdownLanguage, markdownKeymap } from "@codemirror/lang-markdown";
import { livePreview } from "./livepreview.js";

const DEBOUNCE = 1500;
// Marca las transacciones remotas (cargar versión del servidor) para que no disparen autosave.
const remote = Annotation.define();

const TEXTOS = {
  saved: "Guardado",
  saving: "Guardando…",
  offline: "Sin conexión, reintentando",
  conflict: "Pausado: conflicto",
  locked: "Solo lectura",
};

let current = null; // instancia activa (una por nota)

function mount() {
  // Siempre fuera la instancia anterior (navegación parcial incluida).
  if (current) {
    current.destroy();
    current = null;
  }

  const datos = document.getElementById("nota-editable");
  const reader = document.querySelector(".reader");
  const prose = reader && reader.querySelector(".prose");
  if (!datos || !prose) return;

  // htmx puede restaurar del historial una instantánea con el editor ya montado:
  // fuera restos de montajes anteriores (host, indicador, banner) antes de crear los nuestros.
  for (const resto of reader.querySelectorAll(".inline-editor, .inline-status, .inline-banner")) {
    resto.remove();
  }

  let nota;
  try {
    nota = JSON.parse(datos.textContent);
  } catch {
    return;
  }

  const baseUrl = document.body.dataset.base || "";
  const ruta = reader.dataset.note || "";
  const url = baseUrl + "/n/" + ruta.split("/").map(encodeURIComponent).join("/");

  // El HTML del servidor se pinta primero y queda oculto: live.js lo sigue
  // actualizando sin tocar el editor.
  prose.hidden = true;
  const host = document.createElement("div");
  host.className = "prose inline-editor";
  prose.after(host);

  const status = document.createElement("span");
  status.className = "inline-status";
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const meta = reader.querySelector(".meta");
  if (meta) meta.append(status);

  const ctrl = new AbortController();
  const { signal } = ctrl;

  let base = nota.base; // último base confirmado por el servidor
  let saved = nota.raw; // último contenido confirmado
  let inflight = false;
  let again = false;
  let force = "";
  let paused = false;
  let timer = 0;
  let retryTimer = 0;
  let retry = 0;
  let readOnly = !!nota.agent;
  let banner = null;

  const setState = (estado, texto) => {
    status.dataset.state = estado;
    status.textContent = texto || TEXTOS[estado];
  };

  const roComp = new Compartment();
  const roExt = (ro) => [EditorState.readOnly.of(ro), EditorView.editable.of(!ro)];

  const view = new EditorView({
    parent: host,
    doc: nota.raw,
    extensions: [
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      Prec.high(
        keymap.of([
          {
            key: "Mod-s",
            run: () => {
              clearTimeout(timer);
              save();
              return true; // el navegador no abre «Guardar página»
            },
          },
        ])
      ),
      // Sin markdown() para no empaquetar lang-html/css/js.
      markdownLanguage,
      keymap.of(markdownKeymap),
      EditorView.lineWrapping,
      livePreview({ links: nota.links || {}, base: baseUrl }),
      roComp.of(roExt(readOnly)),
      EditorView.contentAttributes.of({
        "aria-label": "Contenido de la nota",
        "aria-multiline": "true",
        spellcheck: "true",
        lang: "es",
      }),
      EditorView.updateListener.of((update) => {
        if (update.docChanged && !update.transactions.some((tr) => tr.annotation(remote))) {
          clearTimeout(timer);
          timer = setTimeout(save, DEBOUNCE);
        }
      }),
    ],
  });

  function quitarBanner() {
    if (banner) {
      banner.remove();
      banner = null;
    }
  }

  // Un solo banner a la vez; si hay botones, el foco va al primero.
  function mostrarBanner(texto, botones) {
    quitarBanner();
    const el = document.createElement("div");
    el.className = "changes inline-banner";
    el.setAttribute("role", "alert");
    el.append(document.createTextNode(texto));
    for (const { label, onClick } of botones) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "btn";
      btn.textContent = label;
      btn.addEventListener("click", onClick, { signal });
      el.append(" ", btn);
    }
    host.before(el);
    banner = el;
    const primero = el.querySelector("button");
    if (primero) primero.focus();
  }

  function bannerConflicto(actual, base409) {
    mostrarBanner("La nota cambió mientras la editabas.", [
      {
        label: "Cargar la versión actual",
        onClick: () => {
          view.dispatch({
            changes: { from: 0, to: view.state.doc.length, insert: actual },
            annotations: [remote.of(true), Transaction.addToHistory.of(false)],
          });
          base = base409;
          saved = view.state.doc.toString(); // el doc queda normalizado a \n
          paused = false;
          force = "";
          quitarBanner();
          setState("saved");
          view.focus();
        },
      },
      {
        label: "Sobrescribir con la mía",
        onClick: () => {
          force = base409;
          paused = false;
          quitarBanner();
          save();
          view.focus();
        },
      },
    ]);
  }

  // Un agente escribe la nota: solo lectura + aviso, hasta nueva navegación.
  function bloquearPorAgente(agente) {
    if (readOnly && banner) return;
    readOnly = true;
    view.dispatch({ effects: roComp.reconfigure(roExt(true)) });
    clearTimeout(timer);
    setState("locked");
    mostrarBanner(`${agente} está escribiendo en esta nota`, []);
  }

  function offline() {
    setState("offline");
    retry = Math.min(retry ? retry * 2 : 2000, 30000);
    clearTimeout(retryTimer);
    if (!signal.aborted) retryTimer = setTimeout(save, retry);
  }

  async function save() {
    if (signal.aborted) return; // instancia destruida: no relanzar nada
    if (paused || readOnly) return;
    if (inflight) {
      again = true;
      return;
    }
    const content = view.state.doc.toString();
    if (content === saved && !force) {
      setState("saved");
      return;
    }
    inflight = true;
    setState("saving");
    try {
      const res = await fetch(url, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(force ? { content, base, force } : { content, base }),
        signal,
        priority: "low", // sync en segundo plano: no compite con la navegación
      });
      const data = await res.json().catch(() => ({}));
      if (res.ok) {
        base = data.base;
        saved = content;
        force = "";
        retry = 0;
        setState("saved");
      } else if (res.status === 409) {
        paused = true;
        setState("conflict");
        bannerConflicto(data.current ?? "", data.base ?? base);
      } else if (res.status === 423) {
        bloquearPorAgente(data.agent || "");
      } else if (res.status === 400 || res.status === 404 || res.status === 413) {
        setState("error", data.error || "Error al guardar"); // no reintenta hasta el siguiente cambio
      } else if (res.status >= 500) {
        offline();
      } else {
        setState("error", data.error || "Error al guardar");
      }
    } catch (err) {
      if (err.name !== "AbortError") offline();
    } finally {
      inflight = false;
      if (again) {
        again = false;
        save();
      }
    }
  }

  // CodeMirror normaliza \r\n a \n al crear el doc: el «guardado» de referencia
  // es el doc normalizado, no el archivo tal cual (si no, Mod-s sin cambios reescribe).
  saved = view.state.doc.toString();

  setState("saved");
  if (nota.agent) bloquearPorAgente(nota.agent);

  current = {
    destroy() {
      clearTimeout(timer);
      clearTimeout(retryTimer);
      ctrl.abort(); // cancela fetch en vuelo y listeners
      view.destroy();
      quitarBanner();
      status.remove();
      host.remove();
      prose.hidden = false; // el HTML del servidor vuelve a ser visible
    },
  };
}

window.scribaInline = { mount };
// En carga completa el script puede evaluarse antes de que exista .prose.
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", mount, { once: true });
} else {
  mount();
}
