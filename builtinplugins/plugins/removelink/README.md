# 清理媒体文件

Go worker for the installed RemoveLink2.18. It monitors configured local hardlink and STRM directories; its index and delayed deletion queue are memory-only, as in source. Source stop/reload immediately flushes remaining queued tasks. Use owned test files until actual application acceptance is complete. The shared cleanup/HOME gates have passed their independent worker and App-fixture checks.

No public command, cron service, HTTP API, page or manual cleanup button is added. The Vue Config and native fallback retain the original13configuration fields and save/cancel behavior. The target supports Linux and local storage.

Development checks from this repository:

```sh
go test ./plugins/removelink ./cmd/remove-link
go test -race ./plugins/removelink ./cmd/remove-link
go vet ./plugins/removelink ./cmd/remove-link
REMOVELINK_VUE_PACKAGE=/path/to/frontend/package.json node --test plugins/removelink/ui_test.mjs
```

The Vue test uses an existing real Vue dev runtime from the provided package location; it installs nothing and does not bundle it into the artifact. Current complete business normal/race checks pass; the original customHOME failure is preserved as historical evidence. Scope and evidence details are in ORIGIN.md; installation/build registry is maintained centrally.
