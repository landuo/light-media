SiteStatistic unread-message control-flow oracle

This is an offline development oracle, not a Python production dependency.
Obtain app/chain/site.py from jxxghp/MoviePilot commit
1528176beacc0f9f8d8d9cbf1adf048b0da67b5c using an authorized source checkout.
The expected SHA256 is
06358a8fd070d46ab254015a2f8c7a34f07c35dd240bd2b3fef38cbf219252fa.
The generator refuses any other bytes and extracts only three reviewed AST
methods; it does not import or execute the rest of MoviePilot.

Run: python3 generate.py --source /path/to/app/chain/site.py > oracle.json
Expected output SHA256:
8b5bfab1ca26549d953e258abaca879d37d6c2dcbab918bba6dfc0b086af6711
63 cases include 59 schema-representable cases, two intentionally unnormalized
coercion probes, one invalid-schema case, and one module-exception control-flow
case. Each result labels its scope. Host, schema, notification and persistence
dependencies are explicit synthetic stubs; this alone is not an end-to-end
parser, database, Router, API or real-account validation. Separate Go tests
exercise those implemented host paths with owned HTTP fixtures.

The pinned source matches the previously frozen installed SiteChain fingerprint.
This does not establish the fingerprint of the complete installed IndexerModule.
No private accounts, production tokens, URLs or responses are embedded.
