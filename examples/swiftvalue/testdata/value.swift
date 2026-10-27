@frozen public struct Record {
    public let text: String
    public let number: Int64
}

@_silgen_name("fixture_value_example_transform")
public func transform(_ value: Record) -> Record {
    Record(text: value.text + "more", number: value.number + 8)
}

@_cdecl("fixture_value_example_metadata")
public func metadata() -> UnsafeRawPointer {
    unsafeBitCast(Record.self, to: UnsafeRawPointer.self)
}

@_silgen_name("fixture_value_example_initialize")
public func initialize(_ destination: UnsafeMutableRawPointer, _ number: Int64) {
    destination.bindMemory(to: Record.self, capacity: 1).initialize(
        to: Record(text: String(repeating: "abcdef", count: 100), number: number))
}

@_silgen_name("fixture_value_example_read")
public func read(_ source: UnsafeRawPointer) -> Int64 {
    let value = source.assumingMemoryBound(to: Record.self).pointee
    return Int64(value.text.utf8.count) + value.number
}
