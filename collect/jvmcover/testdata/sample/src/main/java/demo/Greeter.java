package demo;

public final class Greeter {
    private Greeter() {
    }

    public static String greet(String name, int times) {
        int total = Calculator.add(times, 0);
        return "Hello " + name + " x" + total;
    }
}
