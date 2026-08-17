import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in spagnolo, tradotti da `content/en.ts`. */
export const es: DashboardContent = {
  shell: {
    languageLabel: 'Idioma',
  },

  home: {
    title: 'Resumen',
    intro:
      'Estructura inicial del monorepo. La plantilla Flowbite, el acceso y la '
      + 'gestión de tareas programadas llegan con sus propias issues.',
    backendTitle: 'Backend',
    apiBaseLabel: 'Dirección base de la API',
    check: 'Comprobar el estado del backend',
    checking: 'Comprobando…',
    unreachable: 'Backend no disponible',
  },
}
