Before planning or changing Stratum, read `AGENTS.md` completely and then read every `docs/agent/*.md` file that applies to the affected subsystem. Read the relevant OpenSpec proposal, spec, design, and tasks when present. Trace the complete handler → application → domain/port → infrastructure → wiring call chain before proposing changes.

State assumptions explicitly. Preserve DDD dependency direction, tenant schema compatibility, frozen error response contracts, constant placement rules, and the ban on AI-controlled routing/retry/state machines. Identify protected paths and never modify `config/prod.yaml` or credential files.

Output the exact files, interfaces, tests, migration considerations, and project skills required by the current task.
