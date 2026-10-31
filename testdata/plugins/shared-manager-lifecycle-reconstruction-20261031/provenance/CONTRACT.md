# C5 finite test correction and evidence inheritance

App02 is retained as compile success, five roots executed, four PASS and one FAIL. C5 changes only the ActualConcurrentInitState root's error assertion/comment and removes its now-unused strings import. The other two C test functions, all helpers, B two test functions, product implementation and fixture are byte-identical; test-only.diff is the complete delta. Their four actual App02 PASS results may be inherited at this function/unchanged-dependency scope, not called an unchanged current App02 full closure.

Only TestSharedManagerRuntimeActualConcurrentInitState requires a new execution. It requires an exact existing code-only `plugin: host_operation_failed`, actual source exception and message present in the original immutable capture, and retains state/config/trace/error recovery checks. No SDK or public error detail changed.

The source get_state_callback_error observation calls the raw state getter directly. The second new loop fails that getter after explicit init, so it is a composition of the observed getter error with source-reviewed init ordering, not same-input/event-by-event replay of that source case. The init_init_error branch and successful init traces retain their direct relevant source comparisons. This qualification does not require new source capture or unrelated tests.
