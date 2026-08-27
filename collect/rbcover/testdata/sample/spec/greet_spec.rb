require_relative "spec_helper"
require_relative "../lib/greet"

RSpec.describe Greet do
  it "greets by count" do
    expect(Greet.greet("ada", 1)).to eq("hi ada! hi ada!")
  end
end
