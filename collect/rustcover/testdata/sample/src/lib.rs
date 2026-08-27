pub mod text;

pub fn add(a: i32, b: i32) -> i32 {
    let result = a + b;
    result
}

pub fn sub(a: i32, b: i32) -> i32 {
    let result = a - b;
    result
}

pub fn greet(name: &str, extra: i32) -> String {
    let total = add(extra, 1);
    let decorated = text::decorate(name);
    format!("{decorated} ({total})")
}
