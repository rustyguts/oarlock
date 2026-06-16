-- Add a 'registry' secret type for private container-registry credentials.
-- Distinct from 'api_key' (whose provider whitelist is LLM-only); registry
-- secrets carry no provider, so the existing provider constraint
-- (type <> 'api_key' or provider is not null) still holds for them.
alter table secrets drop constraint secrets_type_check;
alter table secrets add constraint secrets_type_check
  check (type in ('generic', 'api_key', 'registry'));
