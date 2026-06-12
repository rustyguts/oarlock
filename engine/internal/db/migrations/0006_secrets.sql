-- API keys generalize into Secrets: encrypted workspace values usable by any
-- task or workflow ({{secrets.<name>}}), never logged (engine redacts).
-- type 'api_key' = BYOK for ai.* steps (provider required); 'generic' = any
-- sensitive value.
alter table api_keys rename to secrets;
alter table secrets add column type text not null default 'api_key';
alter table secrets add constraint secrets_type_check check (type in ('generic', 'api_key'));
alter table secrets alter column provider drop not null;
alter table secrets add constraint secrets_provider_check
  check (type <> 'api_key' or provider is not null);
