# Compatibility evidence

October Bus names a harness as verified only when a current public evidence record passes the applicable conformance profile.

The registry contains only independently reviewed passing records. Experimental adapter manifests and unreviewed attempt bundles do not count as compatibility evidence.

Each evidence record must validate against [`compatibility-evidence.schema.json`](../spec/0.1/schemas/compatibility-evidence.schema.json) and include the harness version, adapter version, Bus versions, platform, result digest, verification time, repository commit, limitations, and verification mode. The registry itself is validated against [`compatibility-registry.schema.json`](../spec/0.1/schemas/compatibility-registry.schema.json).

`registry.json` contains paths to current passing evidence. Failed or stale records may remain for history but must be removed from the registry.

Use the [harness verification runbook](RUNBOOK.md) to produce a reproducible evidence record.

Missing an account or platform? Use [maintainer-assisted verification](VERIFICATION.md) to request a run and prepare a local, sanitized, unreviewed log bundle. CI validates all formal evidence files, including records not listed in the registry.

The automated `mcp-adapter` runner verifies an adapter executable without asking a model to perform the checks. This establishes the transport and coordination behavior of the adapter. It does not establish compatibility for a named harness. A harness enters this registry only after its released version also completes the runbook through that adapter.

## Support levels

| Level | Meaning |
| --- | --- |
| Experimental | Configuration exists, but the integration has not passed the required profile. |
| Tier 1 | Manual setup has passed discovery and durable messaging checks. |
| Tier 2 | A packaged adapter has passed the complete required profile. |
| Tier 3 | Native integration adds verified host lifecycle or wake behavior. |

Only Tier 2 and Tier 3 integrations may be named as October Bus compatible. Optional capabilities and platform coverage are stated separately.

## Evidence rules

- One harness family counts once. Operating systems, editor modes, and unchanged forks do not create extra entries.
- Evidence expires after 90 days, when its harness integration surface changes, or when its protocol profile is replaced.
- A passing record must identify the exact harness, adapter, runtime, protocol, platform, repository commit, and limitations.
- Manual and assisted runs must include reproducible instructions. Automated runs should include a public workflow or attestation.
- An integration is removed from the verified registry when current evidence no longer passes.
