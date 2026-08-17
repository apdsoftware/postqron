import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in spagnolo, tradotti da `content/en.ts`. */
export const es: DashboardContent = {
  shell: {
    languageLabel: 'Idioma',
    skipToContent: 'Ir al contenido',
    navigationLabel: 'Navegación principal',
    openNavigation: 'Abrir la navegación',
    closeNavigation: 'Cerrar la navegación',
    nav: {
      overview: 'Resumen',
    },
    toLightTheme: 'Cambiar al tema claro',
    toDarkTheme: 'Cambiar al tema oscuro',
  },

  home: {
    title: 'Resumen',
    intro: 'El servicio Postqron que ejecuta tus tareas programadas, y si está respondiendo.',
    backendTitle: 'Estado del servicio',
    apiBaseLabel: 'Dirección base de la API',
    check: 'Comprobar el estado del backend',
    checking: 'Comprobando…',
    unreachable: 'Backend no disponible',
  },

  notFound: {
    title: 'Página no encontrada',
    intro: 'Esta dirección no corresponde a ninguna pantalla del panel. Puede que haya cambiado, o que el enlace sea incorrecto.',
    back: 'Volver al resumen',
  },
}
