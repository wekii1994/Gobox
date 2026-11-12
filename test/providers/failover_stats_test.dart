import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gobox/models/failover_stats.dart';
import 'package:gobox/providers/failover_stats.dart';

void main() {
  ProviderContainer containerFor({
    required Future<FailoverStats> Function() load,
    required Future<FailoverStats> Function() reset,
  }) {
    final container = ProviderContainer(
      overrides: [
        failoverStatsProvider.overrideWith(
          () => FailoverStatsNotifier(loadStats: load, resetStats: reset),
        ),
      ],
    );
    addTearDown(container.dispose);
    return container;
  }

  test(
    'refresh finishing after page disposal does not write disposed state',
    () async {
      final response = Completer<FailoverStats>();
      var reads = 0;
      final container = containerFor(
        load: () => ++reads == 1
            ? Future.value(const FailoverStats(total: 3))
            : response.future,
        reset: () async => const FailoverStats(),
      );
      final subscription = container.listen(failoverStatsProvider, (_, _) {});
      await container.read(failoverStatsProvider.future);
      final refresh = container.read(failoverStatsProvider.notifier).refresh();
      subscription.close();
      await container.pump();
      response.complete(const FailoverStats(total: 4));
      await expectLater(refresh, completes);
    },
  );

  test(
    'reset waits for an older refresh and owns the final displayed result',
    () async {
      final response = Completer<FailoverStats>();
      var reads = 0;
      var resets = 0;
      final container = containerFor(
        load: () => ++reads == 1
            ? Future.value(const FailoverStats(total: 3))
            : response.future,
        reset: () async {
          resets++;
          return const FailoverStats();
        },
      );
      container.listen(failoverStatsProvider, (_, _) {});
      await container.read(failoverStatsProvider.future);
      final notifier = container.read(failoverStatsProvider.notifier);
      final refresh = notifier.refresh();
      final reset = notifier.reset();
      await Future<void>.delayed(Duration.zero);
      expect(resets, 0);
      response.complete(const FailoverStats(total: 3));
      await Future.wait([refresh, reset]);
      expect(resets, 1);
      expect(container.read(failoverStatsProvider).requireValue.total, 0);
    },
  );

  test('a refresh requested after reset reads the cleared data', () async {
    final resetResponse = Completer<FailoverStats>();
    var total = 7;
    var reads = 0;
    final container = containerFor(
      load: () async {
        reads++;
        return FailoverStats(total: total);
      },
      reset: () async {
        final result = await resetResponse.future;
        total = 0;
        return result;
      },
    );
    container.listen(failoverStatsProvider, (_, _) {});
    await container.read(failoverStatsProvider.future);
    final notifier = container.read(failoverStatsProvider.notifier);
    final reset = notifier.reset();
    final refresh = notifier.refresh();
    await Future<void>.delayed(Duration.zero);
    expect(reads, 1);
    resetResponse.complete(const FailoverStats());
    await Future.wait([reset, refresh]);
    expect(reads, 2);
    expect(container.read(failoverStatsProvider).requireValue.total, 0);
  });

  test(
    'reset requested during initial load completes after that load',
    () async {
      final initialResponse = Completer<FailoverStats>();
      var resets = 0;
      final container = containerFor(
        load: () => initialResponse.future,
        reset: () async {
          resets++;
          return const FailoverStats();
        },
      );
      container.listen(failoverStatsProvider, (_, _) {});
      final reset = container.read(failoverStatsProvider.notifier).reset();
      await Future<void>.delayed(Duration.zero);
      expect(resets, 0);
      initialResponse.complete(const FailoverStats(total: 9));
      await reset;
      expect(resets, 1);
      expect(container.read(failoverStatsProvider).requireValue.total, 0);
    },
  );

  test(
    'reset queued behind slow refresh executes before a reopened page loads',
    () async {
      final slowRefresh = Completer<FailoverStats>();
      var backendTotal = 9;
      var reads = 0;
      var resets = 0;
      final container = containerFor(
        load: () async {
          reads++;
          if (reads == 2) {
            return slowRefresh.future;
          }
          return FailoverStats(total: backendTotal);
        },
        reset: () async {
          resets++;
          backendTotal = 0;
          return const FailoverStats();
        },
      );

      // First page mount loads initial count (9), starts slow refresh, queues reset, and closes.
      final sub1 = container.listen(failoverStatsProvider, (_, _) {});
      expect((await container.read(failoverStatsProvider.future)).total, 9);
      final refreshFuture = container
          .read(failoverStatsProvider.notifier)
          .refresh();
      final resetFuture = container
          .read(failoverStatsProvider.notifier)
          .reset();
      await Future<void>.delayed(Duration.zero);
      expect(resets, 0);

      sub1.close();
      await container.pump();

      // Immediately reopen page before slow refresh finishes: reopened page must wait for reset.
      final sub2 = container.listen(failoverStatsProvider, (_, _) {});
      addTearDown(sub2.close);
      await Future<void>.delayed(Duration.zero);
      expect(reads, 2);

      slowRefresh.complete(const FailoverStats(total: 9));
      await Future.wait([refreshFuture, resetFuture]);
      final reopened = await container.read(failoverStatsProvider.future);

      expect(resets, 1);
      expect(reads, 3);
      expect(reopened.total, 0);
      expect(container.read(failoverStatsProvider).requireValue.total, 0);
    },
  );

  test('reopening page while reset IPC is in flight waits for reset before loading', () async {
    final resetInFlight = Completer<FailoverStats>();
    var backendTotal = 7;
    var reads = 0;
    final container = containerFor(
      load: () async {
        reads++;
        return FailoverStats(total: backendTotal);
      },
      reset: () async {
        final result = await resetInFlight.future;
        backendTotal = 0;
        return result;
      },
    );

    final sub1 = container.listen(failoverStatsProvider, (_, _) {});
    expect((await container.read(failoverStatsProvider.future)).total, 7);
    final resetFuture = container.read(failoverStatsProvider.notifier).reset();
    await Future<void>.delayed(Duration.zero);

    sub1.close();
    await container.pump();

    final sub2 = container.listen(failoverStatsProvider, (_, _) {});
    addTearDown(sub2.close);
    await Future<void>.delayed(Duration.zero);
    expect(reads, 1);

    resetInFlight.complete(const FailoverStats());
    await resetFuture;
    final reopened = await container.read(failoverStatsProvider.future);

    expect(reads, 2);
    expect(reopened.total, 0);
    expect(container.read(failoverStatsProvider).requireValue.total, 0);
  });
}
