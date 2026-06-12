-- "Connections" were always just provider API keys — rename to match
-- (UI surfaces them under "Configuration"). Step configs referencing the old
-- "connection" key are rewritten in place.
alter table connections rename to api_keys;

update workflow_versions
set definition = regexp_replace(definition::text, '"connection"\s*:', '"api_key":', 'g')::jsonb
where definition::text ~ '"connection"\s*:';
