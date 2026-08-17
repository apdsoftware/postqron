import type { SiteContent } from '~/types/content'

/**
 * Contenuti del sito pubblico in spagnolo, tradotti da `content/en.ts`.
 *
 * I nomi dei piani, i prezzi e i nomi propri restano quelli della lingua
 * sorgente: sono identificatori commerciali, non testo.
 */
export const es: SiteContent = {
  meta: {
    title: 'Tareas cron fiables, definidas como código',
    description:
      'Describe tus programaciones en un archivo de tu repositorio. Postqron las '
      + 'ejecuta a su hora, reintenta cuando hace falta y siempre te cuenta cómo fue.',
  },

  ui: {
    menu: 'Menú',
    homeLink: 'Postqron, volver al inicio',
    language: 'Idioma',
    emailPlaceholder: 'Escribe tu correo',
    emailSubmit: 'Empezar',
    readMore: 'Leer',
    closeVideo: 'Cerrar',
    photoOf: 'Foto de {name}',
    contactTitle: 'Contacto',
    emailPrefix: 'Correo: ',
    rightsReserved: 'Todos los derechos reservados.',
  },

  legal: {
    sourceNotice: 'Este documento aún no está disponible en español. A continuación se muestra el original en inglés.',
    versionLabel: 'Versión',
    effectiveDateLabel: 'Fecha de entrada en vigor',
  },

  nav: {
    main: [
      { label: 'Inicio', to: '/#welcome' },
      {
        label: 'Producto',
        children: [
          { label: 'Funciones', to: '/#features' },
          { label: 'Testimonios', to: '/#testimonials' },
          { label: 'Precios', to: '/#pricing' },
        ],
      },
      {
        label: 'Recursos',
        children: [
          { label: 'API y webhooks', to: '/#api' },
          { label: 'Del blog', to: '/#blog' },
        ],
      },
      { label: 'Contacto', to: '/#contact' },
    ],
    cta: { label: 'Prueba gratis', to: '/#welcome' },
    footer: [
      {
        title: 'Producto',
        items: [
          { label: 'Funciones', to: '/#features' },
          { label: 'Precios', to: '/#pricing' },
          { label: 'API y webhooks', to: '/#api' },
          { label: 'Blog', to: '/#blog' },
        ],
      },
      {
        title: 'Soporte',
        items: [
          { label: 'Testimonios', to: '/#testimonials' },
          { label: 'Contacto', to: '/#contact' },
        ],
      },
      {
        title: 'Legal',
        items: [
          { label: 'Términos del servicio', to: '/legal/terms-of-service' },
          { label: 'Política de privacidad', to: '/legal/privacy-policy' },
          { label: 'Política de cookies', to: '/legal/cookie-policy' },
          { label: 'Uso aceptable', to: '/legal/acceptable-use-policy' },
        ],
      },
    ],
  },

  company: {
    name: 'Postqron',
    legalName: 'Postqron',
    about:
      'Tareas cron gestionadas, definidas en tu propio repositorio y reunidas en un '
      + 'solo sitio: una programación fiable, el registro de cada ejecución y un aviso '
      + 'cuando algo no arranca.',
    address: 'Dirección por confirmar',
    email: 'soporte@postqron.com',
  },

  // En español el símbolo sigue a la cifra, con espacio: «9 € + IVA».
  money: { currencyPosition: 'after', taxNote: '+ IVA' },

  hero: {
    title: 'Tareas cron fiables, definidas como código',
    text:
      'Describe tus programaciones en un archivo de tu repositorio. Postqron las '
      + 'ejecuta a su hora, reintenta cuando hace falta y siempre te cuenta cómo fue.',
    image: '/img/hero.jpg',
    imageAlt: 'La consola de Postqron con la lista de ejecuciones',
  },

  featuresIntro: {
    title: 'Cuatro cosas que Postqron te quita de la lista',
    lead:
      'Ningún servidor cron que mantener en pie, ningún script que falle en silencio, '
      + 'ninguna duda sobre qué se ejecutó y cuándo.',
  },

  features: [
    {
      icon: 'checkSquare',
      title: 'Todas las tareas en un sitio',
      text: 'Repositorios y entornos distintos, una sola lista.',
    },
    {
      icon: 'github',
      title: 'Definidas en el repositorio',
      text: 'Un cron.yaml, releído en cada push a la rama.',
      highlighted: true,
    },
    {
      icon: 'barChart',
      title: 'Registro de cada ejecución',
      text: 'Duración, resultado y respuesta, mientras ocurren.',
    },
    {
      icon: 'bell',
      title: 'Reintenta y avisa',
      text: 'Backoff ante los fallos, aviso cuando no basta.',
    },
  ],

  showcases: [
    {
      title: 'Tus programaciones viven en tu repositorio',
      text:
        'Un archivo cron.yaml describe tareas, horarios y destinos. En cada push '
        + 'Postqron lo relee y lo realinea todo: la revisión pasa por la pull request.',
      bullets: [
        'Expresiones cron con zona horaria, horario de verano incluido',
        'Modo por intervalos hasta el segundo',
        'Entornos separados para staging y producción',
        'Errores de sintaxis señalados en el commit',
      ],
      image: '/img/screenshots/jobs.png',
      imageAlt: 'Lista de tareas cron sincronizadas desde un repositorio',
      imageWidth: 593,
      imageHeight: 467,
      imageSide: 'left',
    },
    {
      title: 'Siempre sabes cómo fue',
      text:
        'Cada ejecución deja rastro: cuándo empezó, cuánto duró, qué respondió. Los '
        + 'fallos se convierten en un aviso, no en un hallazgo.',
      bullets: [
        'Historial de ejecuciones con filtros por resultado',
        'Duración media y tasa de fallos por tarea',
        'Avisos por correo o webhook en Slack y Discord',
      ],
      image: '/img/screenshots/metrics.png',
      imageAlt: 'Gráfico de la duración de las ejecuciones a lo largo del tiempo',
      imageWidth: 605,
      imageHeight: 375,
      imageSide: 'right',
    },
  ],

  apiBand: {
    text: 'Documentación de la API, la línea de comandos y los webhooks. Elige por dónde empezar.',
    channels: [
      { icon: 'code', label: 'API REST', to: '/#api' },
      { icon: 'terminal', label: 'CLI', to: '/#api' },
      { icon: 'plug', label: 'Webhooks', to: '/#api' },
    ],
  },

  testimonialsIntro: {
    title: 'Quién lo usa',
    lead:
      'Equipos que han dejado de mantener una máquina en pie solo para hacer correr '
      + 'encima unas líneas de crontab.',
  },

  testimonials: [
    {
      name: 'Giulia Tomassini',
      role: 'Backend lead',
      quote:
        'Teníamos tres servidores con tres crontab distintos y ya nadie sabía cuál era '
        + 'el bueno. Ahora la verdad está en el repositorio y la revisamos en una pull '
        + 'request.',
      avatar: '/img/people/1.svg',
      placeholder: true,
    },
    {
      name: 'Marco Renzi',
      role: 'Fundador',
      quote:
        'La tarea nocturna de facturación llevaba dos semanas fallando sin que nos '
        + 'diéramos cuenta. Ahora llega un aviso al segundo intento fallido.',
      avatar: '/img/people/2.svg',
      placeholder: true,
    },
    {
      name: 'Sara Lombardi',
      role: 'Platform engineer',
      quote:
        'El modo por intervalos nos ha quitado un servicio entero: lo que escribía en '
        + 'una cola cada diez segundos ahora lo hace Postqron.',
      avatar: '/img/people/3.svg',
      placeholder: true,
    },
    {
      name: 'Andrea Cesaroni',
      role: 'CTO',
      quote:
        'Staging y producción tienen programaciones distintas y por fin eso ya no es '
        + 'una variable de entorno que alguien tenga que recordar a mano.',
      avatar: '/img/people/4.svg',
      placeholder: true,
    },
    {
      name: 'Davide Ferraro',
      role: 'Desarrollador independiente',
      quote:
        'El plan gratuito cubre todos mis proyectos paralelos. Mover el primero me '
        + 'llevó diez minutos.',
      avatar: '/img/people/5.svg',
      placeholder: true,
    },
    {
      name: 'Elena Nardi',
      role: 'Responsable de producto',
      quote:
        'Los registros de ejecución los mira también quien no toca el código: es lo '
        + 'primero que abrimos cuando no llega un informe.',
      avatar: '/img/people/6.svg',
      placeholder: true,
    },
  ],

  pricingIntro: {
    title: 'Planes',
    lead:
      'Se empieza gratis y se cambia cuando hace falta. Los límites los aplica el '
      + 'motor, no están solo escritos aquí.',
  },

  plans: [
    {
      name: 'Free',
      currency: '€',
      price: '0',
      period: '/mes',
      ctaLabel: 'Empezar gratis',
      ctaTo: '/#welcome',
      features: [
        { label: '20 tareas cron', included: true },
        { label: 'Resolución de 1 minuto', included: true },
        { label: '3 días de registros', included: true },
        { label: '1 repositorio cron.yaml', included: true },
        { label: 'Avisos por correo', included: true },
        { label: 'Staging y producción', included: false },
        { label: 'Depuración con IA y tu clave', included: false },
        { label: 'Roles y permisos', included: false },
        { label: 'Workspaces aislados e IP dedicada', included: false },
      ],
    },
    {
      name: 'Pro',
      currency: '€',
      price: '9',
      period: '/mes',
      featured: true,
      ctaLabel: 'Elegir Pro',
      ctaTo: '/#welcome',
      features: [
        { label: '200 tareas cron', included: true },
        { label: 'Resolución de 10 segundos', included: true },
        { label: '15 días de registros', included: true },
        { label: 'Repositorios ilimitados', included: true },
        { label: 'Avisos en Slack y Discord', included: true },
        { label: 'Staging y producción', included: true },
        { label: 'Depuración con IA y tu clave', included: true },
        { label: 'Roles y permisos', included: false },
        { label: 'Workspaces aislados e IP dedicada', included: false },
      ],
    },
    {
      name: 'Team',
      currency: '€',
      price: '29',
      period: '/mes',
      ctaLabel: 'Elegir Team',
      ctaTo: '/#welcome',
      features: [
        { label: 'Tareas cron ilimitadas', included: true },
        { label: 'Resolución de 1 segundo', included: true },
        { label: '30 días de registros, con exportación', included: true },
        { label: 'Repositorios ilimitados', included: true },
        { label: 'Avisos por miembro y entorno', included: true },
        { label: 'Staging y producción', included: true },
        { label: 'Depuración con IA y tu clave', included: true },
        { label: 'Roles y permisos', included: true },
        { label: 'Workspaces aislados e IP dedicada', included: false },
      ],
    },
    {
      name: 'Agency',
      pricePrefix: 'desde',
      currency: '€',
      price: '79',
      period: '/mes',
      ctaLabel: 'Hablemos',
      ctaTo: '/#contact',
      features: [
        { label: 'Tareas cron ilimitadas', included: true },
        { label: 'Resolución de 1 segundo', included: true },
        { label: '90 días de registros, con exportación', included: true },
        { label: 'Repositorios ilimitados', included: true },
        { label: 'Avisos por miembro y entorno', included: true },
        { label: 'Staging y producción', included: true },
        { label: 'Depuración con IA y tu clave', included: true },
        { label: 'Roles y permisos', included: true },
        { label: 'Workspaces aislados e IP dedicada', included: true },
      ],
    },
  ],

  stats: [
    { value: 1, label: 'Segundo de\nresolución' },
    { value: 3, label: 'Intentos por\ndefecto' },
    { value: 30, label: 'Segundos de\ntiempo límite' },
    { value: 90, label: 'Días de\nretención' },
  ],

  blogIntro: {
    title: 'Del blog',
    lead: 'Notas sobre programación, fiabilidad y el oficio de hacer que las cosas arranquen a su hora.',
  },

  articles: [
    {
      title: 'Por qué un cron en una sola máquina acaba fallándote',
      excerpt:
        'Reinicios, zonas horarias y horario de verano: las tres formas en que una '
        + 'programación que parecía sencilla deja de arrancar sin decírselo a nadie.',
      image: '/img/blog/1.jpg',
      to: '/#blog',
    },
    {
      title: 'Qué poner en cron.yaml y qué dejar en el código',
      excerpt:
        'La programación es configuración; el trabajo no. Dónde pasa la frontera y por '
        + 'qué conviene mantenerla nítida desde la primera tarea.',
      image: '/img/blog/2.jpg',
      to: '/#blog',
    },
    {
      title: 'Reintentar bien: backoff, idempotencia y límites sensatos',
      excerpt:
        'Un intento más puede resolver un fallo pasajero o duplicar un cobro. Cómo '
        + 'elegir la política adecuada para cada tarea.',
      image: '/img/blog/3.jpg',
      to: '/#blog',
    },
  ],
}
