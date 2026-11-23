package web

// Iconos de las vistas de la barra lateral. Viven en Go, como logoSVG, para que Tailwind no escanee los paths.
const (
	svgOpen = `<svg class="vi" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`

	navActividadSVG  = svgOpen + `<path d="M22 12h-4l-3 9L9 3l-3 9H2"></path></svg>`
	navGrafoSVG      = svgOpen + `<circle cx="6" cy="6" r="2.5"></circle><circle cx="18" cy="9" r="2.5"></circle><circle cx="9" cy="18" r="2.5"></circle><path d="M8.3 7.1l7.4 1.3M7 8.4l1.4 7.2M16.4 10.9l-5.6 5.4"></path></svg>`
	navConectoresSVG = svgOpen + `<path d="M12 22v-5M9 8V2M15 8V2M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8z"></path></svg>`
	navAgentesSVG    = svgOpen + `<rect x="3" y="8" width="18" height="12" rx="2"></rect><path d="M12 8V4H8M9 13v2M15 13v2"></path></svg>`
)
