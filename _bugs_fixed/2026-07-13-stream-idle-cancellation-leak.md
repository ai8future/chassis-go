# Idle stream cancellation leak

`work.Stream` blocked on an open idle input channel without selecting on context cancellation. Input acceptance is now cancellation-aware and drains cooperative in-flight work before closing output.
