const { add, sub } = require("../src/calc");

test("adds", () => {
  expect(add(2, 3)).toBe(5);
});

test("subtracts", () => {
  expect(sub(3, 2)).toBe(1);
});
