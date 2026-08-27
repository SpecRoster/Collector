# greet calls into calc so cross-file coverage is observable.
from greet import greeting_length


def test_greeting_length():
    assert greeting_length("bob") == 6
