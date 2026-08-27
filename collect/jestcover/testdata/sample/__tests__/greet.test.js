const { greet } = require("../src/greet");

test("greets by count", () => {
  expect(greet("ada", 1)).toBe("hi ada! hi ada!");
});
