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

  status: {
    loading: 'Cargando…',
    errorTitle: 'Algo ha fallado',
    retry: 'Reintentar',
    errors: {
      network: 'El backend no ha respondido. Comprueba la conexión y vuelve a intentarlo.',
      unauthorized: 'Tu sesión ha caducado. Vuelve a iniciar sesión para continuar.',
      forbidden: 'No tienes acceso a este recurso.',
      notFound: 'Este recurso ya no existe.',
      invalid: 'La solicitud ha sido rechazada. Revisa los datos introducidos.',
      server: 'El backend ha tenido un problema. Inténtalo de nuevo en un momento.',
    },
  },

  home: {
    title: 'Resumen',
    intro: 'El servicio Postqron que ejecuta tus tareas programadas, y si está respondiendo.',
    backendTitle: 'Estado del servicio',
    apiBaseLabel: 'Dirección base de la API',
    statusLabel: 'Estado',
    environmentLabel: 'Entorno',
    versionLabel: 'Versión',
    check: 'Comprobar de nuevo',
  },

  notFound: {
    title: 'Página no encontrada',
    intro: 'Esta dirección no corresponde a ninguna pantalla del panel. Puede que haya cambiado, o que el enlace sea incorrecto.',
    back: 'Volver al resumen',
  },
}
