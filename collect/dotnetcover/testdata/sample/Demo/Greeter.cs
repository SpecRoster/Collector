namespace Demo;

public static class Greeter
{
    // Calls into Calculator so cross-file coverage is observable.
    public static string Greet(string name, int extra)
    {
        var count = Calculator.Add(1, extra);
        return string.Concat(System.Linq.Enumerable.Repeat($"hi {name}! ", count)).TrimEnd();
    }
}
