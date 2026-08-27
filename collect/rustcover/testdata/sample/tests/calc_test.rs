// Integration test binary `calc_test`. The inner module is named after the
// file stem so libtest paths come out as "calc_test::<name>" — SpecRoster's
// convention: an integration test's canonical ID is rooted at its file stem.
mod calc_test {
    use sample::{add, sub};

    #[test]
    fn test_add() {
        assert_eq!(add(2, 3), 5);
    }

    #[test]
    fn test_sub() {
        assert_eq!(sub(5, 3), 2);
    }
}
