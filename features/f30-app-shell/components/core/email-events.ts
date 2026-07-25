import type { AppShellLocale } from './catalogs.ts'

export interface TransactionalEmailCommand {
  channel: 'transactional'
  data: {
    action_url?: string
    detail?: string
    occurred_at: string
  }
  event: string
  idempotency_key: string
  occurred_at: string
  recipient: {
    email: string
    id: string
    locale: AppShellLocale
    name?: string
  }
  template_id: 'welcome' | 'workspace_invitation' | 'account_security'
  template_version: '1.0.0'
}

interface RecipientInput {
  email: string
  id: string
  locale: AppShellLocale
  name?: string
}

function command(
  event: string,
  key: string,
  template: TransactionalEmailCommand['template_id'],
  recipient: RecipientInput,
  occurredAt: string,
  data: Omit<TransactionalEmailCommand['data'], 'occurred_at'> = {},
): TransactionalEmailCommand {
  return {
    event,
    idempotency_key: key,
    channel: 'transactional',
    template_id: template,
    template_version: '1.0.0',
    recipient,
    data: { ...data, occurred_at: occurredAt },
    occurred_at: occurredAt,
  }
}

export function welcomeEmailCommand(
  recipient: RecipientInput,
  occurredAt: string,
  actionUrl: string,
): TransactionalEmailCommand {
  return command(
    'f14.welcome.v1',
    `welcome:${recipient.id}`,
    'welcome',
    recipient,
    occurredAt,
    { action_url: actionUrl },
  )
}

export function workspaceInvitationEmailCommand(
  invitationId: string,
  recipient: RecipientInput,
  occurredAt: string,
  actionUrl: string,
): TransactionalEmailCommand {
  return command(
    'f14.workspace_invitation.v1',
    `workspace-invite:${invitationId}`,
    'workspace_invitation',
    recipient,
    occurredAt,
    { action_url: actionUrl },
  )
}

export function securityEmailCommand(
  eventId: string,
  recipient: RecipientInput,
  occurredAt: string,
  detail: string,
): TransactionalEmailCommand {
  return command(
    'f14.account_security.v1',
    `security:${eventId}`,
    'account_security',
    recipient,
    occurredAt,
    { detail },
  )
}
