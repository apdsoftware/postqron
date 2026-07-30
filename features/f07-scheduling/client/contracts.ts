export const schedulingPostStatuses = [
  "scheduled",
  "publishing",
  "published",
  "failed",
  "cancelled",
] as const;

export type SchedulingPostStatus = (typeof schedulingPostStatuses)[number];

export interface ScheduleInput {
  local_date_time: string;
  time_zone: string;
  utc_offset_minutes?: number;
}

export interface ScheduledPost {
  id: string;
  workspace_id: string;
  draft_id: string;
  channel_ids: string[];
  status: SchedulingPostStatus;
  scheduled_for_utc: string;
  scheduled_local: string;
  time_zone: string;
  utc_offset_minutes: number;
  revision: number;
  duplicated_from_post_id?: string;
  created_at: string;
  updated_at: string;
  cancelled_at?: string;
}

export interface CalendarEntry {
  post_id: string;
  draft_id: string;
  channel_ids: string[];
  status: SchedulingPostStatus;
  scheduled_for_utc: string;
  scheduled_local: string;
  time_zone: string;
  utc_offset_minutes: number;
  revision: number;
}

export interface CalendarResponse {
  entries: CalendarEntry[];
}

export interface SchedulePostRequest {
  draft_id: string;
  channel_ids: string[];
  scheduled_at: ScheduleInput;
}

export interface EditScheduledPostRequest {
  expected_revision: number;
  draft_id: string;
  channel_ids: string[];
}

export interface ReschedulePostRequest {
  expected_revision: number;
  scheduled_at: ScheduleInput;
}

export interface DuplicatePostRequest {
  expected_revision: number;
  scheduled_at?: ScheduleInput;
}

export interface CancelPostRequest {
  expected_revision: number;
}

export interface SchedulingAPIError {
  code: string;
  message: string;
  retryable: boolean;
  field?: string;
  rule?: string;
  field_code?: string;
  field_message?: string;
}

export interface SchedulingErrorResponse {
  error: SchedulingAPIError;
}

export interface SchedulingSessionErrorResponse {
  error: string;
}
