ALTER TABLE f14_email_deliveries
    ADD COLUMN locale text NOT NULL DEFAULT 'en',
    ADD COLUMN preheader text NOT NULL DEFAULT '';

ALTER TABLE f14_email_deliveries
    ADD CONSTRAINT f14_email_locale_check
    CHECK (locale IN ('en', 'it', 'es', 'fr', 'de'));
