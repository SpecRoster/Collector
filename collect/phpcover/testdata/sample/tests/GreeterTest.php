<?php

declare(strict_types=1);

namespace Demo\Tests;

use Demo\Greeter;
use PHPUnit\Framework\TestCase;

final class GreeterTest extends TestCase
{
    public function testGreet(): void
    {
        $this->assertSame('Hello Ada x2', Greeter::greet('Ada', 2));
    }
}
