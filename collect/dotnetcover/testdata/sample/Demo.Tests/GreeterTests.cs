using Demo;
using Xunit;

namespace Demo.Tests;

public class GreeterTests
{
    [Fact]
    public void GreetsByCount()
    {
        Assert.Equal("hi ada! hi ada!", Greeter.Greet("ada", 1));
    }
}
