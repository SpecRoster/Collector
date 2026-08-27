<?php

declare(strict_types=1);

namespace Demo\Tests;

use Demo\Calculator;
use PHPUnit\Framework\TestCase;

final class CalculatorTest extends TestCase
{
    public function testAdd(): void
    {
        $this->assertSame(5, Calculator::add(2, 3));
    }

    public function testSub(): void
    {
        $this->assertSame(1, Calculator::sub(3, 2));
    }
}
