package demo;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class GreeterTest {
    @Test
    void testGreet() {
        assertEquals("Hello Bob x2", Greeter.greet("Bob", 2));
    }
}
