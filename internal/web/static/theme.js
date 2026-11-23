// Tema claro/oscuro/sistema. Corre en el <head>, sin defer, para fijar data-theme antes del primer pintado.
(() => {
  const CLAVE = "scriba-tema", MODOS = ["sistema", "claro", "oscuro"];
  const ATRIB = { claro: "light", oscuro: "dark" };
  const root = document.documentElement;

  const get = () => {
    let m = null;
    try { m = localStorage.getItem(CLAVE); } catch (_) { /* localStorage bloqueado */ }
    return MODOS.includes(m) ? m : "sistema";
  };

  const pintar = (modo) => {
    if (ATRIB[modo]) root.dataset.theme = ATRIB[modo];
    else delete root.dataset.theme;
    root.dataset.modo = modo; // el icono del botón se elige por CSS desde <html>: sin parpadeo antes del DOMContentLoaded
    const b = document.getElementById("tema");
    if (b) {
      b.dataset.modo = modo;
      b.setAttribute("aria-label", "Tema: " + modo);
      b.title = "Tema: " + modo;
    }
  };

  const set = (modo) => {
    if (!MODOS.includes(modo)) return;
    try { localStorage.setItem(CLAVE, modo); } catch (_) { /* sin persistencia */ }
    pintar(modo);
  };

  window.scribaTema = { get, set };
  pintar(get());

  document.addEventListener("DOMContentLoaded", () => {
    pintar(get());
    const b = document.getElementById("tema");
    if (b) b.addEventListener("click", () => set(MODOS[(MODOS.indexOf(get()) + 1) % MODOS.length]));
  });
})();
