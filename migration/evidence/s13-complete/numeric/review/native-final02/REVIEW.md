# S13 numeric — actual fixture / native App normal + race

**ACCEPT exact fixture-normal01 two roots and corrected App-normal02 / App-race02 two roots each.** Not ordinary installed-worker CLI, real daemon, full Python or whole-S13 publication acceptance. No tests/source/network rerun by reviewer.

| Actual gate | Inputs (all three maps + current equal) | Program SHA | Tool |
|---|---:|---|---|
| fixture-normal01 | 4131 | e63fd3723d813ea78ea307c1d51c384cccc4ad790b374661218baefd10fe7fb4 | session49215 b6bd12→0268fe, gate PASS; **outer exit1** from later App01 |
| App-normal02 | 6344 | 4307741416e43e1ac64fe2438ff639c2c03d61bbb625c82ba5f832ac04fa2f4f | session6332 570bef→0b6213 exit0 |
| App-race02 | 6353 | a7ada06d011cc214f4d7eebe591a5a8c7d035c539045853bb277ec112b85ceb2 | session18282 3feb3c→c245b2 exit0 |

Read raw command exits/test2json/receipt result SHAs; all selected roots and four original-case subtests PASS, no skipped/failed tests in accepted individual gates. Independently reparsed complete go-list declarations and overlay mappings, confirmed required actual product/test/embed inputs, generated testmain copy/current SHA and named roots, program current hashes, every frozen input. No cache fallback needed. Race flag present in both go-list and compile. Automatic vet was not run.

Fixture gate is real SDK through the fixed fixture handler but recording HostCaller, not subprocess/HTTP. Both App modes exercise real fixed production native HTTP: original three successes plus invalid fourth case, exact numeric kwargs + number tokens (including 125.0), only selector adaptation; each success session-get then torrent-set (six requests total), invalid ValueError causes zero HTTP, next success on same App recovers. New exact empty-Basic assertion now passes; it does not authorize arbitrary credentials or modify transport. Existing scope/unknown/current-config/wrapped/context/close/no-replay root also passes using its explicitly supplied recording ports. App servers are owned loopback only; no forwarding or daemon.

Original App-normal01 failure is retained intact (`native-attempt01/readback.json`): handler wrongly prohibited the actual existing empty Basic policy; three success cases rejected400, invalid and oldscope passed. Original compiler inputs/binary preserved, no after-test map was fabricated for the failed runner. Initial preexec miss is disclosed, not rewritten. Successor02 changes only this owned fixture condition plus explanatory comment; product/worker/original raw unchanged.

These finite results complete the new component parameter chain, and satisfy CLI02 build prerequisites. Still pending: actual normal fixture/server build closure, ordinary installed dual-host CLI with frozen binaries, and final whole-S13 publication composition. Historical eight-operation credit remains per `s13-parent-closure-precheck01`.
