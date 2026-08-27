// Integration test binary `greet_test` (see calc_test.rs for the stem-module
// convention). greet() calls add() and text::decorate(), so this test covers
// both src/lib.rs and src/text.rs — the cross-file coverage the collector
// must record.
mod greet_test {
    use sample::greet;

    #[test]
    fn test_greet() {
        assert_eq!(greet("World", 1), "Hello, World! (2)");
    }
}
