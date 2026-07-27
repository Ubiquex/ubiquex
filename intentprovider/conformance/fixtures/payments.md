# Payments database

We need a Postgres database for the new payments service.

Provision it like our staging database (`@payments.aws_db_instance.staging`),
but smaller — payments traffic starts low and we don't want to
over-provision from day one.

Keep it in the `payments` stack.
