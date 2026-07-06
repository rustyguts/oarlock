-- Built-in auth (project.md §7): password login with argon2id hashes.
-- NULL password_hash = the account cannot log in; while no user has a
-- password the API reports setup_required and /v1/setup claims the seeded
-- owner in place. must_change_password forces admin-created users through a
-- password change on first login.
alter table users add column password_hash text;
alter table users add column must_change_password boolean not null default false;

-- created_by references were ON DELETE NO ACTION, which would block deleting
-- a user who ever created a workflow/secret/etc. Rewire them all to SET NULL
-- (the rows outlive their creator). Constraint names vary — connections was
-- renamed to secrets keeping its original constraint name — so resolve them
-- from the catalog.
do $$
declare r record;
begin
  for r in
    select tc.table_name, tc.constraint_name
    from information_schema.table_constraints tc
    join information_schema.key_column_usage k
      on k.constraint_name = tc.constraint_name and k.table_schema = tc.table_schema
    where tc.constraint_type = 'FOREIGN KEY'
      and k.column_name = 'created_by'
      and tc.table_schema = 'public'
  loop
    execute format('alter table %I drop constraint %I', r.table_name, r.constraint_name);
    execute format(
      'alter table %I add constraint %I foreign key (created_by) references users(id) on delete set null',
      r.table_name, r.table_name || '_created_by_fkey');
  end loop;
end $$;
