require_relative "calc"

# Calls into Calc so cross-file coverage is observable.
module Greet
  def self.greet(name, extra)
    count = Calc.add(1, extra)
    Array.new(count, "hi #{name}!").join(" ")
  end
end
