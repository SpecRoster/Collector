pub fn decorate(name: &str) -> String {
    let mut out = String::from("Hello, ");
    out.push_str(name);
    out.push('!');
    out
}
