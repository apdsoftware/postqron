import type { DashboardContent } from '~/types/content'

/**
 * Testi della dashboard in **inglese**, lingua sorgente (SPEC §8-bis).
 *
 * Le altre quattro lingue traducono da qui: si scrive in inglese e si traduce,
 * non il contrario. Chi cambia un testo lo cambia prima in questo file, poi
 * negli altri.
 */
export const en: DashboardContent = {
  shell: {
    languageLabel: 'Language',
    skipToContent: 'Skip to content',
    navigationLabel: 'Main navigation',
    openNavigation: 'Open navigation',
    closeNavigation: 'Close navigation',
    nav: {
      overview: 'Overview',
      jobs: 'Cron jobs',
    },
    toLightTheme: 'Switch to light theme',
    toDarkTheme: 'Switch to dark theme',
    account: {
      open: 'Account menu',
      signedInAs: 'Signed in as',
      signOut: 'Sign out',
    },
  },

  status: {
    loading: 'Loading…',
    errorTitle: 'Something went wrong',
    retry: 'Try again',
    errors: {
      network: 'The backend did not answer. Check your connection, then try again.',
      unauthorized: 'Your session has expired. Sign in again to continue.',
      forbidden: 'You do not have access to this.',
      notFound: 'This is no longer here.',
      invalid: 'The request was rejected. Check what you entered.',
      server: 'The backend ran into a problem. Try again in a moment.',
    },
  },

  home: {
    title: 'Overview',
    intro: 'The Postqron service that runs your cron jobs, and whether it is answering.',
    backendTitle: 'Service health',
    apiBaseLabel: 'API base URL',
    statusLabel: 'Status',
    environmentLabel: 'Environment',
    versionLabel: 'Version',
    check: 'Check again',
  },

  notFound: {
    title: 'Page not found',
    intro: 'This address matches no screen of the dashboard. It may have moved, or the link may be wrong.',
    back: 'Back to the overview',
  },

  auth: {
    signIn: {
      title: 'Sign in',
      submit: 'Sign in',
      submitting: 'Signing in…',
      noAccount: 'No account yet?',
      noAccountLink: 'Create one',
      interrupted: 'Your session ended. Sign in again to pick up where you left off.',
      returningTo: 'You will be taken back to the page you asked for.',
    },
    signUp: {
      title: 'Create an account',
      submit: 'Create account',
      submitting: 'Creating…',
      haveAccount: 'Already have an account?',
      haveAccountLink: 'Sign in',
      acceptedTitle: 'Check your inbox',
      acceptedBody: 'If the address can be used, we have sent an email with the next steps.',
      acceptedSignIn: 'Go to sign in',
    },
    fields: {
      email: 'Email',
      password: 'Password',
      fullName: 'Full name',
      passwordHint: 'At least 12 characters.',
    },
    errors: {
      credentials: 'Email or password is not correct.',
      tooManyAttempts: 'Too many attempts. Wait a few minutes, then try again.',
      suspended: 'This account is suspended. Contact support.',
      invalidEmail: 'This email address is not valid.',
      weakPassword: 'This password does not meet the requirement above.',
      unexpected: 'The request could not be completed. Try again in a moment.',
      required: 'Fill in this field.',
    },
  },

  jobs: {
    list: {
      title: 'Cron jobs',
      intro: 'Everything Postqron runs for you, and when it runs next.',
      create: 'New cron job',
      empty: 'No cron jobs yet.',
      emptyHint: 'Create one, and it will start running on the schedule you choose.',

      columnName: 'Name',
      columnSchedule: 'Schedule',
      columnNextRun: 'Next run',
      columnState: 'State',
      columnActions: 'Actions',

      nextRunPending: 'Being scheduled',
      nextRunNone: 'Not scheduled',

      edit: 'Edit',
      runNow: 'Run now',
      running: 'Starting…',
      runQueued: 'Run recorded. The engine will call the target in a moment.',
      pause: 'Pause',
      resume: 'Resume',
      delete: 'Delete',
      deleteTitle: 'Delete this cron job?',
      deleteBody: 'The job and its execution history go away. This cannot be undone.',
      deleteConfirm: 'Delete',
      deleteCancel: 'Keep it',
    },

    form: {
      createTitle: 'New cron job',
      editTitle: 'Edit cron job',
      save: 'Save',
      saving: 'Saving…',
      saved: 'Saved',
      cancel: 'Cancel',
      back: 'Back to cron jobs',

      sectionIdentity: 'Identity',
      sectionSchedule: 'Schedule',
      sectionTarget: 'Target',
      sectionExecution: 'Execution',
      sectionAlerts: 'Alerts',

      managedTitle: 'This job is defined in your repository',
      managedBody: 'Edit it in the cron.yaml it comes from. A change made here would be reverted at the next push, without an error. You can still pause it.',

      invalidTitle: 'Some fields need a look before this can be saved.',
      nameTaken: 'You already have a job with this name. The name is the job\'s stable identity.',
      unexpected: 'The job could not be saved. Try again in a moment.',
    },

    fields: {
      name: 'Name',
      nameHint: 'Letters, digits, dot, dash and underscore. It is the job\'s stable identity: renaming it is the same as deleting it and creating another.',
      description: 'Description',
      optional: 'Optional',

      mode: 'How often',
      modeCron: 'Cron expression',
      modeInterval: 'Fixed interval',
      schedule: 'Cron expression',
      scheduleHint: 'Five fields: minute, hour, day of month, month, day of week.',
      every: 'Every',
      everyUnit: 'Unit',
      timezone: 'Time zone',
      timezoneHint: 'The job\'s own time zone, not your browser\'s. Daylight saving changes are handled in it.',

      environments: 'Environments',
      url: 'URL',
      method: 'Method',
      headers: 'Headers',
      headerName: 'Header name',
      headerValue: 'Header value',
      addHeader: 'Add a header',
      removeHeader: 'Remove this header',
      body: 'Request body',

      timeout: 'Timeout (seconds)',
      timeoutHint: 'How long to wait for the target before giving up.',
      retries: 'Retries',
      backoff: 'Backoff',
      overlap: 'If the previous run is still going',
      overlapHint: 'With sub-minute schedules this is not a rare case, it is the norm.',
      alerts: 'Tell me when it fails',
      enabled: 'Active',
      enabledHint: 'A paused job keeps its history and stops running.',
    },

    options: {
      backoff: {
        exponential: 'Exponential',
        linear: 'Linear',
        fixed: 'Fixed',
      },
      overlap: {
        skip: 'Skip the new run',
        queue: 'Queue it',
        allow: 'Run both',
      },
      overlapHint: {
        skip: 'The occurrence is recorded as skipped, with the reason. Nothing runs twice.',
        queue: 'Runs stay serialised. A job slower than its own interval builds a backlog.',
        allow: 'Both calls go out together. A target that bills per call would bill twice.',
      },
      alerts: {
        email: 'Email',
        slack: 'Slack',
        discord: 'Discord',
        webhook: 'Webhook',
      },
      environments: {
        staging: 'Staging',
        production: 'Production',
      },
      everyUnits: {
        s: 'seconds',
        m: 'minutes',
        h: 'hours',
      },
    },

    preview: {
      title: 'Next runs',
      inTimezone: 'Times shown in {zone}, the job\'s own time zone.',
      epochAnchored: 'Intervals are anchored to the Unix epoch, not to when you save: every 1h fires on the full UTC hour.',
      never: 'This expression never comes round. Check the day and month you asked for.',
      invalid: 'Fix the schedule to see when this job would run.',
      shifted: 'This wall-clock time does not exist on that day — the clock jumps over it. The job runs at the first instant that does exist.',
      ambiguous: 'This wall-clock time happens twice on that day. The job runs at the first one only.',
      scheduled: 'Scheduled by the engine',
    },

    plan: {
      title: 'Your plan',
      jobsUsed: 'Cron jobs: {used} of {limit}',
      jobsUnlimited: 'Cron jobs: {used}, with no hard cap',
      jobsFull: 'Your plan is full. Delete a job, or move to a larger plan, before creating another.',
      minInterval: 'Shortest interval: {value}',
      retention: 'Execution logs kept for {days} days',

      suspendedTitle: 'Some jobs were stopped by a plan change',
      suspendedByJobLimit: 'Stopped because the plan was full: {count}. Turn back on as many as the plan allows — which ones is your call, and only you can make it.',
      suspendedByResolution: 'Stopped because they run more often than {value}: {count}. Change their schedule to bring them back.',
      resolutionBlocked: 'This job runs more often than {value}, which is what your plan allows. Change its schedule first — freeing a slot elsewhere will not bring it back.',

      limitJobs: 'Your plan is full. Delete a job, or move to a larger plan.',
      limitResolution: 'The {plan} plan runs jobs no more often than {value}. Widen the interval, or move to a larger plan.',
      limitEnvironments: 'Your plan has a single environment. Staging needs a larger plan.',
      upgrade: 'See the plans',
    },

    state: {
      active: 'Active',
      paused: 'Paused',
      suspendedByJobLimit: 'Stopped by a plan change',
      suspendedByResolution: 'Too frequent for the plan',
      archived: 'Archived',
      managed: 'From a repository',
    },

    errors: {
      required: 'This field is required.',
      tooLong: 'At most {limit} characters.',
      invalidName: 'Letters, digits, dot, dash and underscore only, starting and ending with a letter or a digit.',
      invalidUrl: 'This is not a readable address.',
      unsupportedScheme: 'Only http and https addresses can be called.',
      invalidHeaderName: 'This is not a valid header name.',
      reservedHeader: '{value} is decided by the runner and cannot be set by the job.',
      duplicateHeader: '{value} appears twice. Only one of the two would be sent.',
      headerNewline: 'A header value cannot contain a line break.',
      tooManyHeaders: 'At most {limit} headers.',
      headerTooLong: '{value} is longer than the {limit} characters allowed.',
      bodyTooLong: 'The body is over {limit} bytes.',
      timeoutRange: 'The timeout goes from {min} to {max} seconds.',
      timeoutWhole: 'The timeout must be a whole number of seconds.',
      retriesRange: 'Retries go from 0 to {limit}.',
      environmentsRequired: 'Pick at least one environment.',
      scheduleRequired: 'Write a cron expression, or switch to a fixed interval.',
      scheduleConflict: 'A cron expression and an interval cannot both be set.',
      scheduleMacro: 'Shorthands like @daily are not accepted. Write the five fields instead.',
      scheduleFieldCount: 'A cron expression has five fields: minute, hour, day of month, month, day of week.',
      scheduleField: '{value} is not something this field accepts.',
      unknownTimezone: '{value} is not a known time zone name.',
      localTimezone: 'A job needs an explicit time zone, not the one of whatever machine runs it.',
      invalidInterval: 'The interval must be a whole number of seconds, one or more.',
      targetNotAllowed: 'This address cannot be called from Postqron.',
      nameTaken: 'You already have a job with this name.',
      rejected: 'The server rejected this field.',
    },
  },
}
