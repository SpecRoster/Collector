# Shared RSpec configuration. Each spec requires the lib file under test via
# require_relative — requiring every lib here would mark all definition lines
# covered in every spec run and defeat per-spec coverage isolation.
RSpec.configure do |config|
  config.expect_with :rspec do |c|
    c.syntax = :expect
  end
end
