<?php

declare(strict_types=1);

namespace Demo;

final class Calculator
{
    public static function add(int $a, int $b): int
    {
        return $a + $b;
    }

    public static function sub(int $a, int $b): int
    {
        return $a - $b;
    }
}
