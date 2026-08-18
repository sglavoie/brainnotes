export type NotePinRequest = {
  key: string;
  token: string;
};

// before and after are note keys, never ranks: the server computes the new rank
// from the pinned list as it is on disk, so a stale browser cannot write an
// order it derived from a list that moved. An empty key is an end of the list.
export type NotePinReorderRequest = NotePinRequest & {
  before: string;
  after: string;
};

export type NotePinFetchRequest = {
  url: string;
  init: RequestInit;
};
