<?php

declare(strict_types=1);

namespace Demo;

final class Greeter
{
    public static function greet(string $name, int $times): string
    {
        $total = Calculator::add($times, 0);

        return sprintf('Hello %s x%d', $name, $total);
    }
}
