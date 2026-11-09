bool _listEquals<T>(List<T>? a, List<T>? b) {
  if (identical(a, b)) return true;
  if (a == null || b == null) return false;
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

class FailoverRecord {
  final int time;
  final String group;
  final String before;
  final String after;

  const FailoverRecord({
    this.time = 0,
    this.group = '',
    this.before = '',
    this.after = '',
  });

  int get timestamp => time ~/ 1000;

  DateTime get dateTime => DateTime.fromMillisecondsSinceEpoch(time);

  factory FailoverRecord.fromJson(Map<String, dynamic> json) {
    final rawTime = (json['time'] as num?)?.toInt() ??
        (json['timestamp'] as num?)?.toInt() ??
        0;
    final timeInMs =
        rawTime > 0 && rawTime < 100000000000 ? rawTime * 1000 : rawTime;
    return FailoverRecord(
      time: timeInMs,
      group: json['group'] as String? ?? '',
      before: json['before'] as String? ?? '',
      after: json['after'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'time': time,
        'group': group,
        'before': before,
        'after': after,
      };

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is FailoverRecord &&
          runtimeType == other.runtimeType &&
          time == other.time &&
          group == other.group &&
          before == other.before &&
          after == other.after;

  @override
  int get hashCode => Object.hash(
        time,
        group,
        before,
        after,
      );
}

class FailoverStats {
  final int total;
  final int today;
  final String date;
  final List<FailoverRecord> records;

  const FailoverStats({
    this.total = 0,
    this.today = 0,
    this.date = '',
    this.records = const [],
  });

  int get totalCount => total;
  int get todayCount => today;
  String get lastDate => date;

  factory FailoverStats.fromJson(Map<String, dynamic> json) {
    final rawRecords = json['records'];
    final list = <FailoverRecord>[];
    if (rawRecords is List) {
      for (final item in rawRecords) {
        if (item is Map) {
          list.add(
            FailoverRecord.fromJson(Map<String, dynamic>.from(item)),
          );
        }
      }
    }
    return FailoverStats(
      total: (json['total'] as num?)?.toInt() ??
          (json['total_count'] as num?)?.toInt() ??
          0,
      today: (json['today'] as num?)?.toInt() ??
          (json['today_count'] as num?)?.toInt() ??
          0,
      date: json['date'] as String? ??
          json['last_date'] as String? ??
          '',
      records: list,
    );
  }

  Map<String, dynamic> toJson() => {
        'total': total,
        'today': today,
        'date': date,
        'records': records.map((e) => e.toJson()).toList(),
      };

  FailoverStats copyWith({
    int? total,
    int? totalCount,
    int? today,
    int? todayCount,
    String? date,
    String? lastDate,
    List<FailoverRecord>? records,
  }) {
    return FailoverStats(
      total: total ?? totalCount ?? this.total,
      today: today ?? todayCount ?? this.today,
      date: date ?? lastDate ?? this.date,
      records: records ?? this.records,
    );
  }

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is FailoverStats &&
          runtimeType == other.runtimeType &&
          total == other.total &&
          today == other.today &&
          date == other.date &&
          _listEquals(records, other.records);

  @override
  int get hashCode => Object.hash(
        total,
        today,
        date,
        Object.hashAll(records),
      );
}
