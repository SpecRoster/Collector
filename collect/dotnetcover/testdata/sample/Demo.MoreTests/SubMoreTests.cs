using Demo;
using Xunit;

namespace Demo.MoreTests;

public class SubMoreTests
{
    [Fact]
    public void SubHandlesNegatives()
    {
        Assert.Equal(-1, Calculator.Sub(1, 2));
    }
}
