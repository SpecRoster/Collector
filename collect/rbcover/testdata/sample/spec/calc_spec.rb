require_relative "spec_helper"
require_relative "../lib/calc"

RSpec.describe Calc do
  it "adds" do
    expect(Calc.add(2, 3)).to eq(5)
  end

  it "subtracts" do
    expect(Calc.sub(3, 2)).to eq(1)
  end
end
