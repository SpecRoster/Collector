using Demo;
using Xunit;

namespace Demo.Tests;

public class CalculatorTests
{
    [Fact]
    public void AddWorks()
    {
        Assert.Equal(5, Calculator.Add(2, 3));
    }

    [Fact]
    public void SubWorks()
    {
        Assert.Equal(1, Calculator.Sub(3, 2));
    }
}
