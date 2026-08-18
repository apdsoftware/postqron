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
    account: {
      open: 'Menú de la cuenta',
      signedInAs: 'Sesión iniciada como',
      signOut: 'Cerrar sesión',
    },
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

  auth: {
    signIn: {
      title: 'Iniciar sesión',
      submit: 'Iniciar sesión',
      submitting: 'Iniciando sesión…',
      noAccount: '¿Todavía no tienes cuenta?',
      noAccountLink: 'Crea una',
      interrupted: 'Tu sesión ha terminado. Vuelve a iniciar sesión para retomar donde lo dejaste.',
      returningTo: 'Volverás a la página que habías pedido.',
    },
    signUp: {
      title: 'Crear una cuenta',
      submit: 'Crear cuenta',
      submitting: 'Creando…',
      haveAccount: '¿Ya tienes una cuenta?',
      haveAccountLink: 'Inicia sesión',
      acceptedTitle: 'Revisa tu correo',
      acceptedBody: 'Si la dirección se puede utilizar, hemos enviado un correo con las instrucciones.',
      acceptedSignIn: 'Ir al inicio de sesión',
    },
    fields: {
      email: 'Correo electrónico',
      password: 'Contraseña',
      fullName: 'Nombre y apellidos',
      passwordHint: 'Al menos 12 caracteres.',
    },
    errors: {
      credentials: 'El correo o la contraseña no son correctos.',
      tooManyAttempts: 'Demasiados intentos. Espera unos minutos y vuelve a intentarlo.',
      suspended: 'Esta cuenta está suspendida. Contacta con soporte.',
      invalidEmail: 'Esta dirección de correo no es válida.',
      weakPassword: 'Esta contraseña no cumple el requisito de arriba.',
      unexpected: 'No se ha podido completar la solicitud. Inténtalo de nuevo en un momento.',
      required: 'Rellena este campo.',
    },
  },
}
