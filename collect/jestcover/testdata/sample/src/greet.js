const { add } = require("./calc");

// Calls into calc so cross-file coverage is observable.
function greet(name, extra) {
  const count = add(1, extra);
  return Array(count).fill(`hi ${name}!`).join(" ");
}

module.exports = { greet };
