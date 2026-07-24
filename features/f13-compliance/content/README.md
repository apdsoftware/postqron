# Legal content gate

Legal copy is kept separate by approval state:

- `placeholders/` contains drafting aids only. Files in that directory are
  never publishable and must use `content_status: placeholder`.
- approved artifacts must be added under
  `approved/<document_key>/<major.minor>.md` only after legal review. Their
  metadata must use `content_status: approved`, include the legal approval
  reference, and match the SHA-256 digest of the exact rendered bytes.

No approved legal text exists in this repository yet. The compliance registry
and database constraints deliberately reject publishing the placeholders.
