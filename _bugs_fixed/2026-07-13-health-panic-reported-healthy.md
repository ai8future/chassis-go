# Panicking health check reported healthy

`health.All` discarded `work.Map` failures, losing names and treating recovered panics or skipped cancelled checks as healthy. It now maps every failure back to its named result, aggregates the errors, and causes the HTTP handler to return 503.
