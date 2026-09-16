export type ExerciseSolution = {
  code: string;
  notes: string[];
};
export const exercises: Record<string, ExerciseSolution> = {
  "memory-store-set": {
    code: `func (s *MemoryStore) Set(key, value []byte) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.values[string(key)] = clone(value)
    return nil
}`,
    notes: [
      "The write lock makes the map mutation safe when connections execute concurrently.",
      "clone establishes ownership: changing the caller's byte slice cannot mutate stored data.",
      "defer keeps the unlock paired with the lock as the method evolves.",
    ],
  },
};
