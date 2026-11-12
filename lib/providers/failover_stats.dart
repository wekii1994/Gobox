import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gobox/clash/clash.dart';
import 'package:gobox/models/models.dart';

final failoverStatsProvider =
    AsyncNotifierProvider.autoDispose<FailoverStatsNotifier, FailoverStats>(
      FailoverStatsNotifier.new,
    );

class FailoverStatsNotifier extends AsyncNotifier<FailoverStats> {
  FailoverStatsNotifier({
    Future<FailoverStats> Function()? loadStats,
    Future<FailoverStats> Function()? resetStats,
  }) : _loadStats = loadStats ?? clashCore.getFailoverStats,
       _resetStats = resetStats ?? clashCore.resetFailoverStats;

  final Future<FailoverStats> Function() _loadStats;
  final Future<FailoverStats> Function() _resetStats;
  Future<void> _pending = Future<void>.value();
  int _requestId = 0;

  @override
  Future<FailoverStats> build() {
    ++_requestId;
    final initialLoad = Future<FailoverStats>.sync(_loadStats);
    // Wait for the initial build as well as previous button actions. In
    // particular, a read requested after reset must see the reset result.
    _pending = initialLoad.then<void>(
      (_) {},
      onError: (Object _, StackTrace _) {},
    );
    return initialLoad;
  }

  Future<void> refresh() => _run(_loadStats);

  Future<void> reset() => _run(_resetStats);

  Future<void> _run(Future<FailoverStats> Function() action) async {
    if (!ref.mounted) return;
    final requestId = ++_requestId;
    state = const AsyncValue.loading();
    final result = _pending.then((_) => AsyncValue.guard(action));
    _pending = result.then<void>((_) {});
    final next = await result;
    // The page can be popped while native IPC is still in flight. A newer
    // queued action also owns the displayed result once it has been requested.
    if (ref.mounted && requestId == _requestId) {
      state = next;
    }
  }
}
