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
}
